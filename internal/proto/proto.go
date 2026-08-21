// Package proto defines the wire format of omo's Unix socket / Windows pipe and every
// verb's argument/response payloads. One JSON request per connection.
package proto

import "encoding/json"

type Request struct {
	AgentID string          `json:"agent_id"`
	Verb    string          `json:"verb"`
	Args    json.RawMessage `json:"args,omitempty"`
}

type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type SendArgs struct {
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Priority string `json:"priority"`
}

type ReadArgs struct {
	ID int64 `json:"id"`
}

type DoneArgs struct {
	Result string `json:"result"`
}

type StepArgs struct {
	Description string `json:"description"`
}

type ContextSaveArgs struct {
	Summary string `json:"summary"`
}

type AgentStatus struct {
	Name          string `json:"name"`
	Role          string `json:"role"`
	State         string `json:"state"`
	JobID         int64  `json:"job_id"`
	Step          string `json:"step"`
	StepUpdatedAt string `json:"step_updated_at,omitempty"`
}

type JobCreateArgs struct {
	Title               string   `json:"title"`
	Goal                string   `json:"goal"`
	Role                string   `json:"role"`
	Model               string   `json:"model"`
	Repo                string   `json:"repo"`
	Parent              int64    `json:"parent"`
	DeveloperModels     []string `json:"developer_models,omitempty"`
	ForceDeveloperModel string   `json:"force_developer_model,omitempty"`
}

type JobIDArgs struct {
	ID int64 `json:"id"`
}

type VerdictArgs struct {
	JobID   int64  `json:"job_id"`
	Verdict string `json:"verdict"` // "merge" | "reject"
	Notes   string `json:"notes"`
}

type ReviewOverrideArgs struct {
	JobID int64  `json:"job_id"`
	Notes string `json:"notes"`
}

type IncidentCreateArgs struct {
	Agent  string `json:"agent"`
	Class  string `json:"class"` // stuck|looping|drifting|too-slow|other
	Detail string `json:"detail"`
}

type IncidentResolveArgs struct {
	ID     int64  `json:"id"`
	Report string `json:"report"`
}

type AgentNameArgs struct {
	Name string `json:"name"`
}

type ConfigReloadResponse struct {
	Models int `json:"models"`
	Repos  int `json:"repos"`
}

type AgentInputArgs struct {
	Name string   `json:"name"`
	Text string   `json:"text,omitempty"`
	Keys []string `json:"keys,omitempty"`
}

type LogTailArgs struct {
	Name  string `json:"name"`
	Lines int    `json:"lines"`
}

type LogTailResponse struct {
	Lines []string `json:"lines"`
}

type ReadyResponse struct {
	Prompt string `json:"prompt"`
	JobID  int64  `json:"job_id"`
}

type WaitResponse struct {
	Reason string `json:"reason"`
}

type WaitArgs struct {
	TimeoutMillis int64 `json:"timeout_millis,omitempty"`
}

type JobCreateResponse struct {
	ID int64 `json:"id"`
}

type IncidentCreateResponse struct {
	ID int64 `json:"id"`
}
