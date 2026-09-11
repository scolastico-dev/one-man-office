package supervisor

import (
	"errors"
	"time"
)

// PluginSnapshot exposes lifecycle state suitable for scheduler plugins
// without granting plugins direct database access.
func (s *Supervisor) PluginSnapshot() map[string]any {
	s.mu.Lock()
	shutdownInProgress := s.shutdownInProgress
	started := s.sessionStarted
	activityAt := s.ceoActivityAt
	activityName := s.ceoActivityName
	s.mu.Unlock()
	startedUnix := int64(0)
	if !started.IsZero() {
		startedUnix = started.Unix()
	}
	snapshot := map[string]any{
		"agents":                 []any{},
		"user_inbox":             []any{},
		"ceo_activity_at_unix":   int64(0),
		"office_path":            s.OfficeDir,
		"office_started_at_unix": startedUnix,
		"shutdown_in_progress":   shutdownInProgress,
	}
	fail := func(err error) map[string]any {
		snapshot["agents"] = []any{}
		snapshot["user_inbox"] = []any{}
		snapshot["ceo_activity_at_unix"] = int64(0)
		snapshot["snapshot_error"] = err.Error()
		return snapshot
	}
	if s.DB == nil || s.Mail == nil {
		return fail(errors.New("supervisor database is unavailable"))
	}
	if activityName == "" || activityName != s.CEOName() {
		activityAt = time.Time{}
	}
	if !activityAt.IsZero() {
		snapshot["ceo_activity_at_unix"] = activityAt.Unix()
	}
	rows, err := s.DB.Query(`
		SELECT a.name, a.role, a.state, a.job_id, a.current_step,
		       COALESCE(unixepoch(a.step_updated_at), 0), unixepoch(a.created_at),
		       COALESCE(j.state, ''), COALESCE(unixepoch(j.updated_at), 0),
		       (SELECT COUNT(*) FROM messages m WHERE m.to_target=a.name AND m.read_at IS NULL)
		FROM agents a LEFT JOIN jobs j ON j.id=a.job_id
		WHERE a.state IN ('spawning','working','waiting')
		ORDER BY a.created_at, a.name`)
	if err != nil {
		return fail(err)
	}
	defer rows.Close()
	agents := []any{}
	for rows.Next() {
		var name, role, state, step, jobState string
		var jobID, stepUpdated, created, jobUpdated int64
		var unread int
		if err := rows.Scan(&name, &role, &state, &jobID, &step, &stepUpdated, &created, &jobState, &jobUpdated, &unread); err != nil {
			return fail(err)
		}
		agents = append(agents, map[string]any{
			"name": name, "role": role, "state": state, "job_id": jobID,
			"step": step, "step_updated_at_unix": stepUpdated, "created_at_unix": created,
			"job_state": jobState, "job_updated_at_unix": jobUpdated, "unread_messages": unread,
		})
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}
	snapshot["agents"] = agents
	inbox, err := s.Mail.UnreadUserMetadata()
	if err != nil {
		return fail(err)
	}
	metadata := make([]any, 0, len(inbox))
	for _, message := range inbox {
		metadata = append(metadata, map[string]any{
			"id": message.ID, "from": message.From, "subject": message.Subject,
			"priority": string(message.Priority), "created_at_unix": message.CreatedAtUnix,
		})
	}
	snapshot["user_inbox"] = metadata
	return snapshot
}
