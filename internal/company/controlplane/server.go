// Package controlplane coordinates capacity and usage for supervised offices.
// Its handler must be served only on a private loopback listener.
package controlplane

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
)

type child struct {
	id       string
	profiles map[string]config.Profile
	leases   map[string]bool
	live     LiveState
}

type Server struct {
	mu       sync.Mutex
	limit    int
	used     int
	children map[string]*child
	usage    *modelusage.Cache
}

func New(limit int, fetcher modelusage.Fetcher, ttl time.Duration) *Server {
	if fetcher == nil {
		fetcher = &modelusage.Client{}
	}
	return &Server{limit: limit, children: map[string]*child{}, usage: modelusage.NewCache(fetcher, ttl)}
}

// Register freezes the child's allowlist without modifying its configuration.
// An empty office directory registers a shell with heartbeat access only.
func (s *Server) Register(childID, officeDir string) (string, error) {
	var profiles map[string]config.Profile
	if officeDir != "" {
		absolute, err := filepath.Abs(officeDir)
		if err != nil {
			return "", err
		}
		officeDir = absolute
		cfg, err := config.LoadReadOnly(filepath.Join(officeDir, ".omo", "omo.yaml"))
		if err != nil {
			return "", err
		}
		profiles = cfg.Models
		// Provider credential roots are interpreted by the child relative
		// to its office cwd, which may differ from the dashboard's cwd.
		for key, profile := range profiles {
			if err := normalizeCredentialRoots(profile, officeDir, runtime.GOOS); err != nil {
				return "", fmt.Errorf("profile %q: %w", key, err)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if childID == "" {
		return "", fmt.Errorf("empty child ID")
	}
	for _, c := range s.children {
		if c.id == childID {
			return "", fmt.Errorf("child already registered")
		}
	}
	token, err := randomID()
	if err != nil {
		return "", err
	}
	s.children[token] = &child{id: childID, profiles: profiles, leases: map[string]bool{}, live: emptyLiveState()}
	return token, nil
}

func (s *Server) RegisterShell(childID string) (string, error) { return s.Register(childID, "") }

// Unregister revokes authorization and releases all leases after child exit.
func (s *Server) Unregister(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.children[token]; c != nil {
		s.used -= len(c.leases)
		delete(s.children, token)
	}
}

func (s *Server) Stats() (used, limit int) { s.mu.Lock(); defer s.mu.Unlock(); return s.used, s.limit }

func randomID() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

type request struct {
	Profile string     `json:"profile,omitempty"`
	Lease   string     `json:"lease,omitempty"`
	Live    *LiveState `json:"-"`
}

type pingRequest struct {
	Agents  []AgentState  `json:"agents"`
	TUI     TUIState      `json:"tui"`
	Actions []ActionState `json:"actions"`
	present bool
}

func (p *pingRequest) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("ping request must be an object")
	}
	type plain pingRequest
	var decoded plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, field := range []string{"agents", "tui", "actions"} {
		if raw, ok := fields[field]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("ping field %q must not be null", field)
		}
	}
	*p = pingRequest(decoded)
	p.present = len(fields) > 0
	return nil
}

func decodePingRequest(dec *json.Decoder) (pingRequest, error) {
	var p pingRequest
	if err := expectObjectStart(dec, "ping request"); err != nil {
		return p, err
	}
	seen := make(map[string]bool, 3)
	for dec.More() {
		key, err := decodeObjectKey(dec)
		if err != nil {
			return p, err
		}
		if seen[key] {
			return p, fmt.Errorf("duplicate ping field %q", key)
		}
		seen[key] = true
		switch key {
		case "agents":
			p.Agents, err = decodeAgentArray(dec)
		case "tui":
			p.TUI, err = decodeTUIState(dec)
		case "actions":
			p.Actions, err = decodeActionArray(dec)
		default:
			return p, fmt.Errorf("unknown ping field %q", key)
		}
		if err != nil {
			return p, err
		}
	}
	if err := expectObjectEnd(dec, "ping request"); err != nil {
		return p, err
	}
	p.present = len(seen) > 0
	return p, nil
}

func decodeAgentArray(dec *json.Decoder) ([]AgentState, error) {
	if err := expectArrayStart(dec, "agents"); err != nil {
		return nil, err
	}
	agents := make([]AgentState, 0, maxLiveAgents)
	for dec.More() {
		agent, err := decodeAgentState(dec)
		if err != nil {
			return nil, err
		}
		if len(agents) < maxLiveAgents {
			agents = append(agents, agent)
		}
	}
	if err := expectArrayEnd(dec, "agents"); err != nil {
		return nil, err
	}
	return agents, nil
}

func decodeAgentState(dec *json.Decoder) (AgentState, error) {
	var agent AgentState
	if err := expectObjectStart(dec, "agent"); err != nil {
		return agent, err
	}
	seen := make(map[string]bool, 5)
	for dec.More() {
		key, err := decodeObjectKey(dec)
		if err != nil {
			return agent, err
		}
		if seen[key] {
			return agent, fmt.Errorf("duplicate agent field %q", key)
		}
		seen[key] = true
		switch key {
		case "name":
			agent.Name, err = decodeLiveString(dec, "agent.name")
		case "role":
			agent.Role, err = decodeLiveString(dec, "agent.role")
		case "state":
			agent.State, err = decodeLiveString(dec, "agent.state")
		case "job_id":
			agent.JobID, err = decodeLiveInt(dec, "agent.job_id")
		case "step":
			agent.Step, err = decodeLiveString(dec, "agent.step")
		default:
			return agent, fmt.Errorf("unknown agent field %q", key)
		}
		if err != nil {
			return agent, err
		}
	}
	if err := expectObjectEnd(dec, "agent"); err != nil {
		return agent, err
	}
	return agent, nil
}

func decodeTUIState(dec *json.Decoder) (TUIState, error) {
	var state TUIState
	if err := expectObjectStart(dec, "tui"); err != nil {
		return state, err
	}
	seen := make(map[string]bool, 2)
	for dec.More() {
		key, err := decodeObjectKey(dec)
		if err != nil {
			return state, err
		}
		if seen[key] {
			return state, fmt.Errorf("duplicate tui field %q", key)
		}
		seen[key] = true
		switch key {
		case "mode":
			state.Mode, err = decodeLiveString(dec, "tui.mode")
		case "peek":
			state.Peek, err = decodeLiveString(dec, "tui.peek")
		default:
			return state, fmt.Errorf("unknown tui field %q", key)
		}
		if err != nil {
			return state, err
		}
	}
	if err := expectObjectEnd(dec, "tui"); err != nil {
		return state, err
	}
	return state, nil
}

func decodeActionArray(dec *json.Decoder) ([]ActionState, error) {
	if err := expectArrayStart(dec, "actions"); err != nil {
		return nil, err
	}
	actions := make([]ActionState, 0, maxLiveActions)
	for dec.More() {
		action, err := decodeActionState(dec)
		if err != nil {
			return nil, err
		}
		if len(actions) < maxLiveActions {
			actions = append(actions, action)
		}
	}
	if err := expectArrayEnd(dec, "actions"); err != nil {
		return nil, err
	}
	return actions, nil
}

func decodeActionState(dec *json.Decoder) (ActionState, error) {
	var action ActionState
	if err := expectObjectStart(dec, "action"); err != nil {
		return action, err
	}
	seen := make(map[string]bool, 4)
	for dec.More() {
		key, err := decodeObjectKey(dec)
		if err != nil {
			return action, err
		}
		if seen[key] {
			return action, fmt.Errorf("duplicate action field %q", key)
		}
		seen[key] = true
		switch key {
		case "plugin":
			action.Plugin, err = decodeLiveString(dec, "action.plugin")
		case "action":
			action.Action, err = decodeLiveString(dec, "action.action")
		case "description":
			action.Description, err = decodeLiveString(dec, "action.description")
		case "args":
			action.Args, err = decodeLiveBool(dec, "action.args")
		default:
			return action, fmt.Errorf("unknown action field %q", key)
		}
		if err != nil {
			return action, err
		}
	}
	if err := expectObjectEnd(dec, "action"); err != nil {
		return action, err
	}
	return action, nil
}

func decodeObjectKey(dec *json.Decoder) (string, error) {
	token, err := dec.Token()
	if err != nil {
		return "", err
	}
	key, ok := token.(string)
	if !ok {
		return "", fmt.Errorf("object field name must be a string")
	}
	return key, nil
}

func decodeLiveString(dec *json.Decoder, field string) (string, error) {
	token, err := dec.Token()
	if err != nil {
		return "", err
	}
	value, ok := token.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return boundLiveString(value), nil
}

func decodeLiveInt(dec *json.Decoder, field string) (int64, error) {
	token, err := dec.Token()
	if err != nil {
		return 0, err
	}
	number, ok := token.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", field)
	}
	value, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", field, err)
	}
	return value, nil
}

func decodeLiveBool(dec *json.Decoder, field string) (bool, error) {
	token, err := dec.Token()
	if err != nil {
		return false, err
	}
	value, ok := token.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", field)
	}
	return value, nil
}

func expectObjectStart(dec *json.Decoder, name string) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("%s must be an object", name)
	}
	return nil
}

func expectObjectEnd(dec *json.Decoder, name string) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '}' {
		return fmt.Errorf("%s must end with an object", name)
	}
	return nil
}

func expectArrayStart(dec *json.Decoder, name string) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '[' {
		return fmt.Errorf("%s must be an array", name)
	}
	return nil
}

func expectArrayEnd(dec *json.Decoder, name string) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != ']' {
		return fmt.Errorf("%s must end with an array", name)
	}
	return nil
}

type response struct {
	Lease    string              `json:"lease,omitempty"`
	Snapshot modelusage.Snapshot `json:"snapshot,omitempty"`
}

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Server) Snapshot(childID string) LiveState {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, child := range s.children {
		if child.id == childID {
			return copyLiveState(child.live)
		}
	}
	return emptyLiveState()
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	c := s.children[token]
	if c == nil {
		s.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.Unlock()
	var req request
	if r.Body != nil && r.ContentLength != 0 {
		// /ping is decoded as a stream without an aggregate size limit: valid
		// live-state values may be arbitrarily large, while normalization below
		// retains only the bounded snapshot. Other request types keep their
		// small transport limit.
		var reader io.Reader = r.Body
		if r.URL.Path != "/ping" {
			reader = http.MaxBytesReader(w, r.Body, 4096)
		}
		dec := json.NewDecoder(reader)
		dec.DisallowUnknownFields()
		if r.URL.Path == "/ping" {
			dec.UseNumber()
			ping, err := decodePingRequest(dec)
			if err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if ping.present {
				req.Live = &LiveState{Agents: ping.Agents, TUI: ping.TUI, Actions: ping.Actions}
			}
		} else if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid trailing data", http.StatusBadRequest)
			return
		}
	}
	// Never hold the aggregate lock while reading from a child. Recheck
	// registration after decoding because the process may have exited.
	s.mu.Lock()
	c = s.children[token]
	if c == nil {
		s.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/ping":
		if req.Live != nil {
			c.live = normalizeLiveState(*req.Live)
		}
		s.mu.Unlock()
		writeJSON(w, response{})
	case "/acquire":
		if c.profiles == nil {
			s.mu.Unlock()
			http.Error(w, "shell cannot acquire agent leases", http.StatusForbidden)
			return
		}
		if s.limit <= 0 || s.used >= s.limit {
			s.mu.Unlock()
			http.Error(w, "aggregate agent limit reached", http.StatusTooManyRequests)
			return
		}
		lease, err := randomID()
		if err != nil {
			s.mu.Unlock()
			http.Error(w, "lease creation failed", http.StatusInternalServerError)
			return
		}
		c.leases[lease] = true
		s.used++
		s.mu.Unlock()
		writeJSON(w, response{Lease: lease})
	case "/release":
		if !c.leases[req.Lease] {
			s.mu.Unlock()
			http.Error(w, "unknown lease", http.StatusForbidden)
			return
		}
		delete(c.leases, req.Lease)
		s.used--
		s.mu.Unlock()
		writeJSON(w, response{})
	case "/usage":
		profile, ok := c.profiles[req.Profile]
		s.mu.Unlock()
		if !ok || !modelusage.Metered(profile) {
			http.Error(w, "profile not registered for usage", http.StatusForbidden)
			return
		}
		// The parent owns freshness. Child refresh timers share the same TTL,
		// rather than each bypassing the cache and multiplying provider calls.
		snapshot, err := s.usage.Fetch(r.Context(), req.Profile, profile)
		if err != nil {
			http.Error(w, "provider usage unavailable", http.StatusBadGateway)
			return
		}
		writeJSON(w, response{Snapshot: snapshot})
	default:
		s.mu.Unlock()
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
