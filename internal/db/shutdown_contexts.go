package db

import "database/sql"

type ShutdownContext struct {
	ID        int64
	Role      string
	JobID     int64
	Agent     string
	Context   string
	CreatedAt string
}

func SaveShutdownContext(q Queryer, value ShutdownContext) error {
	_, err := q.Exec(`INSERT INTO shutdown_contexts (role, job_id, agent, context)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent) DO UPDATE SET
		role = excluded.role, job_id = excluded.job_id,
		context = excluded.context, created_at = datetime('now')`,
		value.Role, value.JobID, value.Agent, value.Context)
	return err
}

func GetShutdownContext(q Queryer, role string, jobID int64) (*ShutdownContext, error) {
	var value ShutdownContext
	err := q.QueryRow(`SELECT id, role, job_id, agent, context, created_at
		FROM shutdown_contexts WHERE role = ? AND job_id = ? ORDER BY id LIMIT 1`, role, jobID).
		Scan(&value.ID, &value.Role, &value.JobID, &value.Agent, &value.Context, &value.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &value, err
}

func HasShutdownContextForAgent(q Queryer, agent string) (bool, error) {
	var count int
	err := q.QueryRow(`SELECT COUNT(*) FROM shutdown_contexts WHERE agent = ?`, agent).Scan(&count)
	return count > 0, err
}

func DeleteShutdownContext(q Queryer, id int64) error {
	_, err := q.Exec(`DELETE FROM shutdown_contexts WHERE id = ?`, id)
	return err
}
