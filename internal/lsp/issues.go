// Package lsp monitors registered language servers in managed agent worktrees.
// It exposes diagnostics independently of agent notification transports.
package lsp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// Position is a zero-based LSP position (UTF-16 code units).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open LSP source range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Issue identifies one diagnostic for the lifetime of a worktree runtime.
// File is slash-separated and relative to Worktree. Code may be text or numeric.
type Issue struct {
	ID       string `json:"id"`
	Worktree string `json:"worktree"`
	Server   string `json:"server"`
	File     string `json:"file"`
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// Report contains newly observed, still-current, unsuppressed issues. Sessions
// names the sessions attached when those issues arrived, never later arrivals.
type Report struct {
	Worktree string   `json:"worktree"`
	Sessions []string `json:"sessions"`
	Issues   []Issue  `json:"issues"`
}
type diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     any    `json:"code"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}
type pendingIssue struct {
	issue    Issue
	sessions map[string]bool
}
type issues struct {
	mu         sync.Mutex
	dispatch   sync.Mutex
	root       string
	debounce   time.Duration
	report     func(Report)
	current    map[string]map[string]Issue
	pending    map[string]pendingIssue
	ignored    map[string]bool
	sessions   map[string]bool
	timer      *time.Timer
	generation uint64
	closed     bool
}

func newIssues(root string, debounce time.Duration, report func(Report)) *issues {
	return &issues{root: root, debounce: debounce, report: report, current: map[string]map[string]Issue{}, pending: map[string]pendingIssue{}, ignored: map[string]bool{}, sessions: map[string]bool{}}
}
func (s *issues) attach(session string) { s.mu.Lock(); defer s.mu.Unlock(); s.sessions[session] = true }
func (s *issues) detach(session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, session)
	for id, p := range s.pending {
		delete(p.sessions, session)
		s.pending[id] = p
	}
}
func (s *issues) update(server, file string, ds []diagnostic, baseline bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	key := server + "\x00" + file
	old := s.current[key]
	next := map[string]Issue{}
	changed := false
	for _, d := range ds {
		i := Issue{Worktree: s.root, Server: server, File: file, Range: d.Range, Severity: d.Severity, Code: d.Code, Source: d.Source, Message: d.Message}
		b, _ := json.Marshal(i)
		hash := sha256.Sum256(b)
		i.ID = hex.EncodeToString(hash[:16])
		next[i.ID] = i
		if _, exists := old[i.ID]; !exists && !baseline && !s.ignored[i.ID] {
			recipients := map[string]bool{}
			for id := range s.sessions {
				recipients[id] = true
			}
			s.pending[i.ID] = pendingIssue{i, recipients}
			changed = true
		}
	}
	for id := range old {
		if _, ok := next[id]; !ok {
			delete(s.pending, id)
			changed = true
		}
	}
	s.current[key] = next
	if changed && len(s.pending) > 0 {
		s.generation++
		generation := s.generation
		if s.timer != nil {
			s.timer.Stop()
		}
		s.timer = time.AfterFunc(s.debounce, func() { s.flush(generation) })
	}
}
func (s *issues) flush(generation uint64) {
	s.dispatch.Lock()
	defer s.dispatch.Unlock()
	s.mu.Lock()
	if s.closed || generation != s.generation {
		s.mu.Unlock()
		return
	}
	// Group by recipient so late subscribers cannot inherit an older issue from
	// another issue in the same debounce batch.
	bySession := map[string][]Issue{}
	for _, p := range s.pending {
		for id := range p.sessions {
			if s.sessions[id] {
				bySession[id] = append(bySession[id], p.issue)
			}
		}
	}
	s.pending = map[string]pendingIssue{}
	s.mu.Unlock()
	ids := make([]string, 0, len(bySession))
	for id := range bySession {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	groups := map[string]*Report{}
	for _, id := range ids {
		items := bySession[id]
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		key := ""
		for _, i := range items {
			key += i.ID
		}
		if r := groups[key]; r != nil {
			r.Sessions = append(r.Sessions, id)
		} else {
			groups[key] = &Report{s.root, []string{id}, items}
		}
	}
	for _, r := range groups {
		s.report(*r)
	}
}
func (s *issues) snapshot() []Issue {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Issue{}
	for _, set := range s.current {
		for _, i := range set {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *issues) ignore(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, set := range s.current {
		if _, ok := set[id]; ok {
			s.ignored[id] = true
			delete(s.pending, id)
			return true
		}
	}
	return s.ignored[id]
}
func (s *issues) close() {
	s.dispatch.Lock()
	defer s.dispatch.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.pending = map[string]pendingIssue{}
}
