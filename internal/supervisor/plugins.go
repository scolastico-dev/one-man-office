package supervisor

import (
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/bus"
)

// PluginSnapshot exposes lifecycle state suitable for scheduler plugins
// without granting plugins direct database access.
func (s *Supervisor) PluginSnapshot() map[string]any {
	rows, err := s.DB.Query(`
		SELECT a.name, a.role, a.state, a.job_id, a.current_step,
		       COALESCE(unixepoch(a.step_updated_at), 0), unixepoch(a.created_at),
		       COALESCE(j.state, ''), COALESCE(unixepoch(j.updated_at), 0),
		       (SELECT COUNT(*) FROM messages m WHERE m.to_target=a.name AND m.read_at IS NULL)
		FROM agents a LEFT JOIN jobs j ON j.id=a.job_id
		WHERE a.state IN ('spawning','working','waiting')
		ORDER BY a.created_at, a.name`)
	if err != nil {
		return map[string]any{"agents": []any{}, "snapshot_error": err.Error()}
	}
	defer rows.Close()
	agents := []any{}
	for rows.Next() {
		var name, role, state, step, jobState string
		var jobID, stepUpdated, created, jobUpdated int64
		var unread int
		if err := rows.Scan(&name, &role, &state, &jobID, &step, &stepUpdated, &created, &jobState, &jobUpdated, &unread); err != nil {
			return map[string]any{"agents": []any{}, "snapshot_error": err.Error()}
		}
		agents = append(agents, map[string]any{
			"name": name, "role": role, "state": state, "job_id": jobID,
			"step": step, "step_updated_at_unix": stepUpdated, "created_at_unix": created,
			"job_state": jobState, "job_updated_at_unix": jobUpdated, "unread_messages": unread,
		})
	}
	if err := rows.Err(); err != nil {
		return map[string]any{"agents": []any{}, "snapshot_error": err.Error()}
	}
	return map[string]any{"agents": agents}
}

// PluginNudge sends a durable system reminder through normal mail delivery,
// preserving wait wakeups and the user's terminal-input debounce.
func (s *Supervisor) PluginNudge(agent, message string) error {
	message = strings.TrimSpace(message)
	if message == "" || len(message) > 2000 {
		return fmt.Errorf("plugin nudge must contain 1 to 2000 bytes")
	}
	_, err := s.Mail.Send(bus.SystemSender, agent, "Workflow reminder", message, bus.PrioNormal)
	return err
}
