package bus

import "database/sql"

// DBDirectory resolves org relationships from the agents and jobs tables.
// PM lineage: a developer job's parent_job is its PM's spec job.
type DBDirectory struct {
	DB *sql.DB
}

const living = "('spawning','working','waiting')"

func (d DBDirectory) Role(agent string) (string, bool) {
	var role string
	err := d.DB.QueryRow(
		`SELECT role FROM agents WHERE name = ? AND state IN `+living, agent).Scan(&role)
	return role, err == nil
}

func (d DBDirectory) names(query string, args ...any) []string {
	rows, err := d.DB.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && n != "" {
			out = append(out, n)
		}
	}
	return out
}

func (d DBDirectory) LivingAgents() []string {
	return d.names(`SELECT name FROM agents WHERE state IN ` + living + ` ORDER BY created_at`)
}

func (d DBDirectory) AgentsByRole(role string) []string {
	return d.names(`SELECT name FROM agents WHERE role = ? AND state IN `+living, role)
}

func (d DBDirectory) CEO() (string, bool) {
	names := d.AgentsByRole("ceo")
	if len(names) == 0 {
		return "", false
	}
	return names[0], true
}

func (d DBDirectory) PMOf(developer string) (string, bool) {
	var pm string
	err := d.DB.QueryRow(
		`SELECT pj.assignee FROM agents a
		 JOIN jobs j ON j.id = a.job_id
		 JOIN jobs pj ON pj.id = j.parent_job
		 WHERE a.name = ?`, developer).Scan(&pm)
	return pm, err == nil && pm != ""
}

func (d DBDirectory) DevelopersOf(pm string) []string {
	return d.names(
		`SELECT j.assignee FROM jobs pj
		 JOIN jobs j ON j.parent_job = pj.id
		 WHERE pj.assignee = ? AND j.role = 'developer' AND j.assignee != ''`, pm)
}

func (d DBDirectory) ReviewTargets(reviewer string) (string, string, bool) {
	var dev string
	var pm sql.NullString
	err := d.DB.QueryRow(
		`SELECT j.assignee, pj.assignee FROM agents a
		 JOIN jobs j ON j.id = a.job_id
		 LEFT JOIN jobs pj ON pj.id = j.parent_job
		 WHERE a.name = ? AND a.role = 'reviewer'`, reviewer).Scan(&dev, &pm)
	if err != nil || dev == "" {
		return "", "", false
	}
	return dev, pm.String, true
}

func (d DBDirectory) ReviewerOf(developer string) (string, bool) {
	var reviewer string
	err := d.DB.QueryRow(
		`SELECT r.name FROM agents d JOIN agents r ON r.job_id = d.job_id
		 WHERE d.name = ? AND r.role = 'reviewer' AND r.state IN `+living+`
		 ORDER BY r.created_at DESC LIMIT 1`, developer).Scan(&reviewer)
	return reviewer, err == nil && reviewer != ""
}

// FirefighterContacted reports whether this firefighter previously opened a
// direct mail thread with the agent. That contact grants a narrow reply path.
func (d DBDirectory) FirefighterContacted(firefighter, agent string) bool {
	var found int
	err := d.DB.QueryRow(
		`SELECT 1 FROM messages m
		 JOIN agents f ON f.name = m.from_agent AND f.role = 'firefighter'
		 WHERE m.from_agent = ? AND m.to_target = ? LIMIT 1`, firefighter, agent).Scan(&found)
	return err == nil
}
