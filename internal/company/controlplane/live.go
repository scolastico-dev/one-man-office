package controlplane

import (
	"strings"
	"unicode/utf8"
)

const (
	maxLiveAgents      = 256
	maxLiveActions     = 128
	maxLiveStringBytes = 256
	maxPingBodyBytes   = 64 << 20
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
