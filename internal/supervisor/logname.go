package supervisor

import (
	"fmt"
	"time"
)

// LogName builds a session log filename: the spawn timestamp followed by the
// agent name, which already carries the role (developer-jason), giving
// yyyy-mm-dd_hh-mm-<role>-<name>.log. Sorting the directory therefore orders
// sessions by time, and every spawn keeps its own transcript.
func LogName(agent string) string {
	return fmt.Sprintf("%s-%s.log", time.Now().Format("2006-01-02_15-04"), agent)
}
