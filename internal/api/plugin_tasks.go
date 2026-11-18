package api

// PluginTask is an in-memory process owned by one plugin. ID identifies a
// single run; Name may be reused. Output is a bounded, cleaned combined tail.
type PluginTask struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Ticket    int      `json:"ticket,omitempty"`
	StartedAt string   `json:"started_at"`
	EndedAt   string   `json:"ended_at,omitempty"`
	State     string   `json:"state"`
	Reason    string   `json:"reason,omitempty"`
	Error     string   `json:"error,omitempty"`
	Output    []string `json:"output"`
}

// PluginTasks always carries an array, including when no tasks exist.
type PluginTasks struct {
	Tasks []PluginTask `json:"tasks"`
}

// PluginTasksChangedData invalidates one plugin's task list. Clients refetch
// the snapshot on this event and after reconnect/resync.

type PluginTasksChangedData struct {
	Name string `json:"name"`
}
