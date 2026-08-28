package db

import (
	"database/sql"
	"time"
)

// Agent states: spawning → working ⇄ waiting → done | dead.
type Agent struct {
	Name          string
	Role          string
	Profile       string
	JobID         int64
	Goal          string
	WorkDir       string
	Step          string
	StepUpdatedAt sql.NullString
	State         string
}

const livingStates = "('spawning','working','waiting')"

func InsertAgent(q Queryer, a Agent) error {
	_, err := q.Exec(
		`INSERT INTO agents (name, role, profile, job_id, goal, workdir) VALUES (?,?,?,?,?,?)`,
		a.Name, a.Role, a.Profile, a.JobID, a.Goal, a.WorkDir)
	return err
}

func GetAgent(q Queryer, name string) (*Agent, error) {
	var a Agent
	err := q.QueryRow(
		`SELECT name, role, profile, job_id, goal, workdir, current_step, step_updated_at, state FROM agents WHERE name = ?`, name).
		Scan(&a.Name, &a.Role, &a.Profile, &a.JobID, &a.Goal, &a.WorkDir, &a.Step, &a.StepUpdatedAt, &a.State)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func SetAgentState(q Queryer, name, state string) error {
	if state == "done" || state == "dead" {
		_, err := q.Exec(`UPDATE agents SET state = ?, ended_at = datetime('now') WHERE name = ?`, state, name)
		return err
	}
	_, err := q.Exec(`UPDATE agents SET state = ? WHERE name = ?`, state, name)
	return err
}

func scanAgents(rows *sql.Rows) ([]Agent, error) {
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.Name, &a.Role, &a.Profile, &a.JobID, &a.Goal, &a.WorkDir, &a.Step, &a.StepUpdatedAt, &a.State); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func LivingAgents(q Queryer) ([]Agent, error) {
	rows, err := q.Query(
		`SELECT name, role, profile, job_id, goal, workdir, current_step, step_updated_at, state FROM agents WHERE state IN ` + livingStates + ` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	return scanAgents(rows)
}

func LivingByRole(q Queryer, role string) ([]Agent, error) {
	rows, err := q.Query(
		`SELECT name, role, profile, job_id, goal, workdir, current_step, step_updated_at, state FROM agents WHERE role = ? AND state IN `+livingStates, role)
	if err != nil {
		return nil, err
	}
	return scanAgents(rows)
}

func CountLivingByRole(q Queryer, role string) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM agents WHERE role = ? AND state IN `+livingStates, role).Scan(&n)
	return n, err
}

func LivingByJobRole(q Queryer, jobID int64, role string) ([]Agent, error) {
	rows, err := q.Query(
		`SELECT name, role, profile, job_id, goal, workdir, current_step, step_updated_at, state FROM agents WHERE job_id = ? AND role = ? AND state IN `+livingStates+` ORDER BY created_at DESC`,
		jobID, role)
	if err != nil {
		return nil, err
	}
	return scanAgents(rows)
}

func SetAgentStep(q Queryer, name, step string) error {
	_, err := q.Exec(`UPDATE agents SET current_step = ?, step_updated_at = datetime('now') WHERE name = ?`, step, name)
	return err
}

// AgentStatusUpdatedAt returns the last explicit step update, or the agent's
// creation time when it has not published a step yet.
func AgentStatusUpdatedAt(q Queryer, name string) (time.Time, error) {
	var raw string
	if err := q.QueryRow(`SELECT COALESCE(step_updated_at, created_at) FROM agents WHERE name = ?`, name).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	return time.Parse("2006-01-02 15:04:05", raw)
}

func MarkAllAgentsDead(q Queryer) error {
	_, err := q.Exec(`UPDATE agents SET state = 'dead', ended_at = datetime('now') WHERE state IN ` + livingStates)
	return err
}
