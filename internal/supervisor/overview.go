package supervisor

import (
	"database/sql"
	"sort"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

type RoleCount struct{ Active, Total int }

type ModelTime struct{ Active, Idle time.Duration }

type SessionStats struct {
	Started           time.Time
	Messages          int
	AgentsByRole      map[string]int
	ReviewsStarted    int
	ReviewsRejected   int
	ReviewsMerged     int
	ReviewsOverridden int
	Models            map[string]ModelTime
	CEO               ModelTime
}

type AgentRow struct {
	Name      string
	Role      string
	JobTitle  string
	State     string
	Step      string
	LastEvent string
	Parent    string
	Depth     int
}

// Overview returns one row per living agent for the TUI.
func (s *Supervisor) Overview() []AgentRow {
	agents, err := db.LivingAgents(s.DB)
	if err != nil {
		return nil
	}
	events, _ := db.LastEvents(s.DB, 50)
	lastByAgent := map[string]string{}
	for _, e := range events { // newest first: keep the first hit per agent
		if e.Agent != "" && lastByAgent[e.Agent] == "" {
			lastByAgent[e.Agent] = e.Kind
		}
	}
	jobs, _ := s.Jobs.List()
	jobsByID := make(map[int64]*queue.Job, len(jobs))
	for _, j := range jobs {
		jobsByID[j.ID] = j
	}
	var rows []AgentRow
	for _, a := range agents {
		row := AgentRow{Name: a.Name, Role: a.Role, State: a.State, Step: a.Step, LastEvent: lastByAgent[a.Name]}
		if a.JobID != 0 {
			if j, err := s.Jobs.Get(a.JobID); err == nil {
				row.JobTitle = j.Title
			}
		}
		rows = append(rows, row)
	}
	return arrangeAgentTree(rows, agents, jobsByID)
}

// arrangeAgentTree turns the flat living-agent list into a stable org chart.
// Job lineage records the important spawn relationships: the CEO creates
// top-level work, PM jobs parent their developer jobs, and a reviewer shares
// the developer's job. Agents whose lineage is unavailable remain roots.
func arrangeAgentTree(rows []AgentRow, agents []db.Agent, jobs map[int64]*queue.Job) []AgentRow {
	if len(rows) < 2 {
		return rows
	}
	ceo := ""
	byJobRole := map[int64]map[string]string{}
	for _, a := range agents {
		if a.Role == "ceo" && ceo == "" {
			ceo = a.Name
		}
		if byJobRole[a.JobID] == nil {
			byJobRole[a.JobID] = map[string]string{}
		}
		if byJobRole[a.JobID][a.Role] == "" {
			byJobRole[a.JobID][a.Role] = a.Name
		}
	}
	for i := range rows {
		r := &rows[i]
		switch r.Role {
		case "ceo":
			continue
		case "reviewer":
			for jobID, roles := range byJobRole {
				if roles["reviewer"] == r.Name {
					r.Parent = roles["developer"]
					if r.Parent == "" {
						r.Parent = jobParentAgent(jobID, jobs, byJobRole, ceo)
					}
					break
				}
			}
		default:
			for jobID, roles := range byJobRole {
				if roles[r.Role] == r.Name {
					r.Parent = jobParentAgent(jobID, jobs, byJobRole, ceo)
					break
				}
			}
		}
		if r.Parent == "" && ceo != "" {
			r.Parent = ceo
		}
	}

	children := map[string][]int{}
	known := make(map[string]bool, len(rows))
	for i := range rows {
		known[rows[i].Name] = true
	}
	for i := range rows {
		if rows[i].Parent == rows[i].Name || !known[rows[i].Parent] {
			rows[i].Parent = ""
		}
		children[rows[i].Parent] = append(children[rows[i].Parent], i)
	}
	ordered := make([]AgentRow, 0, len(rows))
	seen := map[string]bool{}
	var visit func(int, int)
	visit = func(i, depth int) {
		if seen[rows[i].Name] {
			return
		}
		seen[rows[i].Name] = true
		rows[i].Depth = depth
		ordered = append(ordered, rows[i])
		for _, child := range children[rows[i].Name] {
			visit(child, depth+1)
		}
	}
	for _, root := range children[""] {
		visit(root, 0)
	}
	// A corrupt cycle must not make a living agent disappear from the TUI.
	for i := range rows {
		visit(i, 0)
	}
	return ordered
}

func jobParentAgent(jobID int64, jobs map[int64]*queue.Job, agents map[int64]map[string]string, ceo string) string {
	j := jobs[jobID]
	if j == nil || j.ParentJob == 0 {
		return ceo
	}
	if roles := agents[j.ParentJob]; roles != nil {
		if parent := roles["product_manager"]; parent != "" {
			return parent
		}
		for _, parent := range roles {
			if parent != "" {
				return parent
			}
		}
	}
	return ceo
}

func (s *Supervisor) QueueStats() (queued, running int) {
	if jobs, err := s.Jobs.List(queue.StateQueued); err == nil {
		queued = len(jobs)
	}
	if jobs, err := s.Jobs.List(queue.StateAssigned, queue.StateWorking, queue.StateReview, queue.StateMerging, queue.StateRework); err == nil {
		running = len(jobs)
	}
	return
}

func (s *Supervisor) OpenIncidents() int {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'open'`).Scan(&n)
	return n
}

func (s *Supervisor) CEOName() string {
	agents, err := db.LivingByRole(s.DB, "ceo")
	if err != nil || len(agents) == 0 {
		return ""
	}
	return agents[0].Name
}

func (s *Supervisor) RoleCounts() map[string]RoleCount {
	out := make(map[string]RoleCount, len(config.AllRoles))
	agents, _ := db.LivingAgents(s.DB)
	for _, a := range agents {
		v := out[a.Role]
		v.Total++
		if a.State != "waiting" {
			v.Active++
		}
		out[a.Role] = v
	}
	return out
}

func (s *Supervisor) BeginSession() {
	s.mu.Lock()
	s.sessionStarted = time.Now()
	s.sessionEventID, _ = db.LastEventID(s.DB)
	s.ceoActivityName = ""
	s.ceoActivityLast = time.Time{}
	s.ceoActivityLog = logSignature{}
	s.ceoActivityActive = 0
	s.ceoActivityIdle = 0
	s.mu.Unlock()
}

func (s *Supervisor) SessionStats() SessionStats {
	s.mu.Lock()
	started, eventID := s.sessionStarted, s.sessionEventID
	ceo := ModelTime{Active: s.ceoActivityActive, Idle: s.ceoActivityIdle}
	s.mu.Unlock()
	stats := SessionStats{Started: started, AgentsByRole: map[string]int{}, Models: map[string]ModelTime{}, CEO: ceo}
	since := started.UTC().Format("2006-01-02 15:04:05")
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE created_at >= ?`, since).Scan(&stats.Messages)
	rows, err := s.DB.Query(`SELECT role, COUNT(*) FROM agents WHERE created_at >= ? GROUP BY role`, since)
	if err == nil {
		for rows.Next() {
			var role string
			var n int
			if rows.Scan(&role, &n) == nil {
				stats.AgentsByRole[role] = n
			}
		}
		rows.Close()
	}
	events, _ := db.EventsSince(s.DB, eventID)
	for _, e := range events {
		switch e.Kind {
		case "review_started":
			stats.ReviewsStarted++
		case "job_rejected":
			stats.ReviewsRejected++
		case "job_merged":
			stats.ReviewsMerged++
		case "review_overridden":
			stats.ReviewsOverridden++
		}
	}
	s.accumulateModelTimes(&stats, since, events)
	return stats
}

type timedAgent struct {
	name, profile, state, created string
	ended                         sql.NullString
}

func (s *Supervisor) accumulateModelTimes(stats *SessionStats, since string, events []db.Event) {
	rows, err := s.DB.Query(`SELECT name, profile, state, created_at, ended_at FROM agents WHERE role != 'ceo' AND created_at >= ?`, since)
	if err != nil {
		return
	}
	var agents []timedAgent
	for rows.Next() {
		var a timedAgent
		if rows.Scan(&a.name, &a.profile, &a.state, &a.created, &a.ended) == nil {
			agents = append(agents, a)
		}
	}
	rows.Close()
	byAgent := map[string][]db.Event{}
	for _, e := range events {
		if e.Agent != "" {
			byAgent[e.Agent] = append(byAgent[e.Agent], e)
		}
	}
	now := time.Now()
	for _, a := range agents {
		state := "spawning"
		last := parseDBTime(a.created)
		var active, idle time.Duration
		for _, e := range byAgent[a.name] {
			t := parseDBTime(e.CreatedAt)
			if t.Before(last) {
				continue
			}
			switch state {
			case "active":
				active += t.Sub(last)
			case "idle":
				idle += t.Sub(last)
			}
			switch e.Kind {
			case "agent_ready", "agent_woken":
				state = "active"
			case "agent_waiting":
				state = "idle"
			case "agent_done", "agent_died":
				state = "ended"
			}
			last = t
		}
		end := now
		if a.ended.Valid {
			end = parseDBTime(a.ended.String)
		}
		if end.After(last) {
			switch state {
			case "active":
				active += end.Sub(last)
			case "idle":
				idle += end.Sub(last)
			}
		}
		v := stats.Models[a.profile]
		v.Active += active
		v.Idle += idle
		stats.Models[a.profile] = v
	}
}

func parseDBTime(v string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04:05", v, time.UTC)
	return t
}

func SortedRoleNames(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
