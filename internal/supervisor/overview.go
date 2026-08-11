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

type ModelTime struct {
	AgentsStarted int
	Active        time.Duration
	Idle          time.Duration
}

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
	s.ceoStatsActive = 0
	s.ceoStatsIdle = 0
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
	s.accumulateModelStarts(&stats, since)
	return stats
}

func (s *Supervisor) accumulateModelStarts(stats *SessionStats, since string) {
	rows, err := s.DB.Query(`SELECT profile, COUNT(*) FROM agents WHERE role != 'ceo' AND created_at >= ? GROUP BY profile`, since)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var profile string
		var count int
		if rows.Scan(&profile, &count) == nil {
			value := stats.Models[profile]
			value.AgentsStarted = count
			stats.Models[profile] = value
		}
	}
}

// OverallStats returns all-time counts plus the latest durable per-model
// snapshot written by StatisticsLoop.
func (s *Supervisor) OverallStats() SessionStats {
	stats := SessionStats{AgentsByRole: map[string]int{}, Models: map[string]ModelTime{}}
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&stats.Messages)
	rows, err := s.DB.Query(`SELECT role, COUNT(*) FROM agents GROUP BY role`)
	if err == nil {
		for rows.Next() {
			var role string
			var count int
			if rows.Scan(&role, &count) == nil {
				stats.AgentsByRole[role] = count
			}
		}
		rows.Close()
	}
	events, _ := db.EventsSince(s.DB, 0)
	for _, event := range events {
		switch event.Kind {
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
	if stored, err := db.OverallStatistics(s.DB); err == nil {
		for _, row := range stored {
			stats.Models[row.Model] = ModelTime{AgentsStarted: row.AgentsStarted, Active: row.Active, Idle: row.Idle}
			stats.CEO.Active += row.CEOActive
			stats.CEO.Idle += row.CEOIdle
		}
	}
	return stats
}

// PersistOverallStatistics recalculates cumulative worker-model time and
// upserts one row per model. Recalculation makes periodic writes idempotent.
func (s *Supervisor) PersistOverallStatistics() error {
	s.statisticsMu.Lock()
	defer s.statisticsMu.Unlock()

	stats := SessionStats{Models: map[string]ModelTime{}}
	events, err := db.EventsSince(s.DB, 0)
	if err != nil {
		return err
	}
	s.accumulateModelTimes(&stats, "", events)
	stored, err := db.OverallStatistics(s.DB)
	if err != nil {
		return err
	}
	byModel := make(map[string]db.ModelStatistics, len(stored)+len(stats.Models))
	for _, row := range stored {
		byModel[row.Model] = row
	}
	for model, value := range stats.Models {
		row := byModel[model]
		row.Model, row.Active, row.Idle = model, value.Active, value.Idle
		byModel[model] = row
	}
	counts, err := s.DB.Query(`SELECT profile, COUNT(*) FROM agents GROUP BY profile`)
	if err != nil {
		return err
	}
	for counts.Next() {
		var model string
		var count int
		if counts.Scan(&model, &count) == nil {
			row := byModel[model]
			row.Model, row.AgentsStarted = model, count
			byModel[model] = row
		}
	}
	if err := counts.Err(); err != nil {
		counts.Close()
		return err
	}
	if err := counts.Close(); err != nil {
		return err
	}

	s.mu.Lock()
	since := s.sessionStarted.UTC().Format("2006-01-02 15:04:05")
	ceoActive, ceoIdle := s.ceoActivityActive, s.ceoActivityIdle
	persistedActive, persistedIdle := s.ceoStatsActive, s.ceoStatsIdle
	s.mu.Unlock()
	var ceoModel string
	_ = s.DB.QueryRow(`SELECT profile FROM agents WHERE role = 'ceo' AND created_at >= ? ORDER BY created_at DESC LIMIT 1`, since).Scan(&ceoModel)
	if ceoModel != "" {
		row := byModel[ceoModel]
		row.Model = ceoModel
		if ceoActive > persistedActive {
			row.CEOActive += ceoActive - persistedActive
		}
		if ceoIdle > persistedIdle {
			row.CEOIdle += ceoIdle - persistedIdle
		}
		byModel[ceoModel] = row
	}
	rows := make([]db.ModelStatistics, 0, len(byModel))
	for _, row := range byModel {
		rows = append(rows, row)
	}
	if err := db.UpsertOverallStatistics(s.DB, rows); err != nil {
		return err
	}
	s.mu.Lock()
	s.ceoStatsActive, s.ceoStatsIdle = ceoActive, ceoIdle
	s.mu.Unlock()
	return nil
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
