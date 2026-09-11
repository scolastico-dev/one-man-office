package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	maxLiveAgents      = 256
	maxLiveActions     = 128
	maxLiveStringBytes = 256
)

type AgentState struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	State string `json:"state"`
	JobID int64  `json:"job_id"`
	Step  string `json:"step"`
}

type TUIState struct {
	Mode string `json:"mode"`
	Peek string `json:"peek"`
}

type ActionState struct {
	Plugin      string `json:"plugin"`
	Action      string `json:"action"`
	Description string `json:"description"`
	Args        bool   `json:"args"`
}

type LiveState struct {
	Agents  []AgentState  `json:"agents"`
	TUI     TUIState      `json:"tui"`
	Actions []ActionState `json:"actions"`
}

func (a *AgentState) UnmarshalJSON(data []byte) error {
	type plain AgentState
	var decoded plain
	fields, err := decodeLiveObject(data, &decoded)
	if err != nil {
		return err
	}
	if err := rejectNullLiveFields(fields, "name", "role", "state", "job_id", "step"); err != nil {
		return err
	}
	*a = AgentState(decoded)
	return nil
}

func (t *TUIState) UnmarshalJSON(data []byte) error {
	type plain TUIState
	var decoded plain
	fields, err := decodeLiveObject(data, &decoded)
	if err != nil {
		return err
	}
	if err := rejectNullLiveFields(fields, "mode", "peek"); err != nil {
		return err
	}
	*t = TUIState(decoded)
	return nil
}

func (a *ActionState) UnmarshalJSON(data []byte) error {
	type plain ActionState
	var decoded plain
	fields, err := decodeLiveObject(data, &decoded)
	if err != nil {
		return err
	}
	if err := rejectNullLiveFields(fields, "plugin", "action", "description", "args"); err != nil {
		return err
	}
	*a = ActionState(decoded)
	return nil
}

func decodeLiveObject(data []byte, dst any) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("live state value must be an object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("live state value has trailing data")
		}
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func rejectNullLiveFields(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		raw, ok := fields[name]
		if ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("live state field %q must not be null", name)
		}
	}
	return nil
}

func emptyLiveState() LiveState {
	return LiveState{Agents: []AgentState{}, Actions: []ActionState{}}
}

func copyLiveState(state LiveState) LiveState {
	state.Agents = append([]AgentState(nil), state.Agents...)
	state.Actions = append([]ActionState(nil), state.Actions...)
	if state.Agents == nil {
		state.Agents = []AgentState{}
	}
	if state.Actions == nil {
		state.Actions = []ActionState{}
	}
	return state
}

func normalizeLiveState(state LiveState) LiveState {
	if len(state.Agents) > maxLiveAgents {
		state.Agents = state.Agents[:maxLiveAgents]
	}
	if len(state.Actions) > maxLiveActions {
		state.Actions = state.Actions[:maxLiveActions]
	}
	state = copyLiveState(state)
	for i := range state.Agents {
		state.Agents[i].Name = boundLiveString(state.Agents[i].Name)
		state.Agents[i].Role = boundLiveString(state.Agents[i].Role)
		state.Agents[i].State = boundLiveString(state.Agents[i].State)
		state.Agents[i].Step = boundLiveString(state.Agents[i].Step)
	}
	state.TUI.Mode = boundLiveString(state.TUI.Mode)
	state.TUI.Peek = boundLiveString(state.TUI.Peek)
	for i := range state.Actions {
		state.Actions[i].Plugin = boundLiveString(state.Actions[i].Plugin)
		state.Actions[i].Action = boundLiveString(state.Actions[i].Action)
		state.Actions[i].Description = boundLiveString(state.Actions[i].Description)
	}
	return state
}

func boundLiveString(value string) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= maxLiveStringBytes {
		return value
	}
	raw := []byte(value[:maxLiveStringBytes])
	for !utf8.Valid(raw) {
		raw = raw[:len(raw)-1]
	}
	return string(raw)
}
