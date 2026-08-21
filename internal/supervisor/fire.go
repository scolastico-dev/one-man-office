package supervisor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func (s *Supervisor) gateFirefighter(agentID string) (*db.Agent, error) {
	a, err := db.GetAgent(s.DB, agentID)
	if err != nil {
		return nil, err
	}
	if a.Role != "firefighter" {
		return nil, fmt.Errorf("only the firefighter may do this")
	}
	return a, nil
}

func (s *Supervisor) gateManagement(agentID string) (string, error) {
	if agentID == "user" {
		return "user", nil
	}
	a, err := db.GetAgent(s.DB, agentID)
	if err != nil {
		return "", err
	}
	if a.Role != "ceo" && a.Role != "firefighter" {
		return "", fmt.Errorf("only the user, CEO, or firefighter may do this")
	}
	return a.Name, nil
}

func (s *Supervisor) registerFireVerbs(srv *sockd.Server) {
	srv.Handle("office.estop", func(agentID string, _ json.RawMessage) (any, error) {
		caller := agentID
		if agentID != "user" {
			a, err := db.GetAgent(s.DB, agentID)
			if err != nil {
				return nil, err
			}
			if a.Role != "ceo" && a.Role != "firefighter" {
				return nil, fmt.Errorf("only the user, CEO, or firefighter may emergency-stop omo")
			}
			caller = a.Name
		}
		db.AppendEvent(s.DB, "office_emergency_stop", caller, 0, "")
		// Let the socket acknowledgement reach the caller before shutdown closes
		// the listener and database underneath this handler.
		go func() {
			time.Sleep(10 * time.Millisecond)
			s.requestEmergencyStop()
		}()
		return nil, nil
	})

	srv.Handle("office.pause", func(agentID string, _ json.RawMessage) (any, error) {
		caller, err := s.gateManagement(agentID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.firefighterPaused = true
		s.mu.Unlock()
		db.AppendEvent(s.DB, "office_paused", caller, 0, "")
		return nil, nil
	})
	srv.Handle("office.resume", func(agentID string, _ json.RawMessage) (any, error) {
		caller, err := s.gateManagement(agentID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.firefighterPaused = false
		s.mu.Unlock()
		db.AppendEvent(s.DB, "office_resumed", caller, 0, "")
		s.kickDispatch()
		go s.resumePendingReviews()
		return nil, nil
	})
	srv.Handle("office.halt-spawns", func(agentID string, _ json.RawMessage) (any, error) {
		caller, err := s.gateManagement(agentID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.ceoSpawnHalted = true
		s.mu.Unlock()
		db.AppendEvent(s.DB, "spawning_halted", caller, 0, "by management")
		return nil, nil
	})
	srv.Handle("office.resume-spawns", func(agentID string, _ json.RawMessage) (any, error) {
		caller, err := s.gateManagement(agentID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.ceoSpawnHalted = false
		s.mu.Unlock()
		db.AppendEvent(s.DB, "spawning_resumed", caller, 0, "by management")
		s.kickDispatch()
		go s.resumePendingReviews()
		return nil, nil
	})

	kill := func(agentID string, args json.RawMessage) (any, error) {
		caller, err := s.gateManagement(agentID)
		if err != nil {
			return nil, err
		}
		var a proto.AgentNameArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		var victims []string
		if agents, _ := db.LivingByRole(s.DB, a.Name); len(agents) > 0 {
			for _, v := range agents {
				victims = append(victims, v.Name)
			}
		} else if _, err := db.GetAgent(s.DB, a.Name); err == nil {
			victims = []string{a.Name}
		} else {
			return nil, fmt.Errorf("no agent or role %q", a.Name)
		}
		killed := 0
		for _, v := range victims {
			if v == caller {
				continue // never self-terminate via kill
			}
			if err := s.StopAgent(v, caller, "kill"); err != nil {
				return nil, err
			}
			killed++
		}
		return map[string]int{"killed": killed}, nil
	}
	srv.Handle("agent.kill", kill)
	srv.Handle("agent.restart", kill) // requeue + dispatch IS the restart

	srv.Handle("job.cancel", func(agentID string, args json.RawMessage) (any, error) {
		if _, err := s.gateManagement(agentID); err != nil {
			return nil, err
		}
		var a proto.JobIDArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return nil, s.CancelJob(a.ID, agentID)
	})
	srv.Handle("job.requeue", func(agentID string, args json.RawMessage) (any, error) {
		if _, err := s.gateManagement(agentID); err != nil {
			return nil, err
		}
		var a proto.JobIDArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return nil, s.RequeueJob(a.ID, agentID)
	})
	srv.Handle("incident.resolve", func(agentID string, args json.RawMessage) (any, error) {
		caller, err := s.gateManagement(agentID)
		if err != nil {
			return nil, err
		}
		var a proto.IncidentResolveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if a.Report == "" {
			return nil, fmt.Errorf("--report is required")
		}
		res, err := s.DB.Exec(
			`UPDATE incidents SET state = 'resolved', resolved_at = datetime('now') WHERE id = ? AND state = 'open'`, a.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, fmt.Errorf("incident %d not open", a.ID)
		}
		db.AppendEvent(s.DB, "incident_resolved", caller, 0, fmt.Sprintf("#%d", a.ID))
		if _, err := s.Mail.Send(caller, "user",
			fmt.Sprintf("incident %d resolved", a.ID), a.Report, bus.PrioHigh); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

// nextOpenIncident spawns a firefighter for the oldest open incident, if any.
func (s *Supervisor) nextOpenIncident() {
	var id int64
	if err := s.DB.QueryRow(
		`SELECT id FROM incidents WHERE state = 'open' ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		return
	}
	if n, _ := db.CountLivingByRole(s.DB, "firefighter"); n > 0 {
		return
	}
	s.spawnFirefighter(id)
}
