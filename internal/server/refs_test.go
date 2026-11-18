package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// refsRace is one ticket write racing one settings removal.
type refsRace struct {
	s      *Server
	write  func() *http.Response
	remove func() *http.Response
	// The ticket write is stopped after this lookup (kind and id, see
	// refsLock.looked).
	kind, id string
	// missing, when set, is the project folder the race removes from disk:
	// while the ticket write is stopped, or before the removal starts.
	missing           string
	writeOK, removeOK int
}

// refsRaces builds a fresh server for each case of a ticket write against a
// removal: create and update, of a project and of a model, against the
// single and the bulk project removal and both profile routes.
func refsRaces(t *testing.T) map[string]func(t *testing.T) refsRace {
	withProfile := func(t *testing.T, s *Server, b *browser) {
		t.Helper()
		wantStatus(t, asUser(b, s, "PUT", "/api/profiles/claude", testProfile("a", "b")).StatusCode, http.StatusOK)
	}
	newTaskOn := func(t *testing.T, s *Server, b *browser, project string) int {
		t.Helper()
		resp := asUser(b, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{project}})
		wantStatus(t, resp.StatusCode, http.StatusCreated)
		return body[api.Ticket](t, resp).Number
	}
	return map[string]func(t *testing.T) refsRace{
		"create a ticket of a removed project": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			p := addProject(t, s)
			return refsRace{
				s: s,
				write: func() *http.Response {
					return asUser(b, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{p}})
				},
				remove: func() *http.Response { return asUser(b, s, "DELETE", "/api/projects/"+p, nil) },
				kind:   "project", id: p,
				writeOK: http.StatusCreated, removeOK: http.StatusNoContent,
			}
		},
		"create a ticket of a project removed as missing": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			p := addProject(t, s)
			pr, _ := s.settings.Project(p)
			return refsRace{
				s: s,
				write: func() *http.Response {
					return asUser(b, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{p}})
				},
				remove: func() *http.Response { return asUser(b, s, "DELETE", "/api/projects?missing=true", nil) },
				kind:   "branch", id: pr.Path, missing: pr.Path,
				writeOK: http.StatusCreated, removeOK: http.StatusOK,
			}
		},
		"create a ticket of a dropped model": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			withProfile(t, s, b)
			p := addProject(t, s)
			return refsRace{
				s: s,
				write: func() *http.Response {
					return asUser(b, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{p}, Model: "claude/b"})
				},
				remove: func() *http.Response { return asUser(b, s, "PUT", "/api/profiles/claude", testProfile("a")) },
				kind:   "model", id: "claude/b",
				writeOK: http.StatusCreated, removeOK: http.StatusOK,
			}
		},
		"create a ticket of a removed profile": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			withProfile(t, s, b)
			p := addProject(t, s)
			return refsRace{
				s: s,
				write: func() *http.Response {
					return asUser(b, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{p}, Model: "claude/b"})
				},
				remove: func() *http.Response { return asUser(b, s, "DELETE", "/api/profiles/claude", nil) },
				kind:   "model", id: "claude/b",
				writeOK: http.StatusCreated, removeOK: http.StatusNoContent,
			}
		},
		"create a ticket of a profile removed with its roles detached": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			withProfile(t, s, b)
			wantStatus(t, asUser(b, s, "PUT", "/api/roles/developer", api.RoleSettings{Models: []string{"claude/b"}, Assignment: "random"}).StatusCode, http.StatusOK)
			p := addProject(t, s)
			return refsRace{
				s: s,
				write: func() *http.Response {
					return asUser(b, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{p}, Model: "claude/b"})
				},
				remove: func() *http.Response { return asUser(b, s, "DELETE", "/api/profiles/claude?detach=true", nil) },
				kind:   "model", id: "claude/b",
				writeOK: http.StatusCreated, removeOK: http.StatusOK,
			}
		},
		"move a ticket to a removed project": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			p1, p2 := addProject(t, s), addProject(t, s)
			n := newTaskOn(t, s, b, p1)
			return refsRace{
				s:      s,
				write:  func() *http.Response { return asUser(b, s, "PATCH", tp(n), map[string]any{"projects": []string{p2}}) },
				remove: func() *http.Response { return asUser(b, s, "DELETE", "/api/projects/"+p2, nil) },
				kind:   "project", id: p2,
				writeOK: http.StatusOK, removeOK: http.StatusNoContent,
			}
		},
		"move a ticket to a project removed as missing": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			p1, p2 := addProject(t, s), addProject(t, s)
			pr, _ := s.settings.Project(p2)
			n := newTaskOn(t, s, b, p1)
			return refsRace{
				s:      s,
				write:  func() *http.Response { return asUser(b, s, "PATCH", tp(n), map[string]any{"projects": []string{p2}}) },
				remove: func() *http.Response { return asUser(b, s, "DELETE", "/api/projects?missing=true", nil) },
				kind:   "branch", id: pr.Path, missing: pr.Path,
				writeOK: http.StatusOK, removeOK: http.StatusOK,
			}
		},
		"set a dropped model on a ticket": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			withProfile(t, s, b)
			n := newTaskOn(t, s, b, addProject(t, s))
			return refsRace{
				s:      s,
				write:  func() *http.Response { return asUser(b, s, "PATCH", tp(n), map[string]any{"model": "claude/b"}) },
				remove: func() *http.Response { return asUser(b, s, "PUT", "/api/profiles/claude", testProfile("a")) },
				kind:   "model", id: "claude/b",
				writeOK: http.StatusOK, removeOK: http.StatusOK,
			}
		},
		"set a model of a removed profile on a ticket": func(t *testing.T) refsRace {
			s := newTestServer(t)
			b := newBrowser(t, s)
			withProfile(t, s, b)
			n := newTaskOn(t, s, b, addProject(t, s))
			return refsRace{
				s:      s,
				write:  func() *http.Response { return asUser(b, s, "PATCH", tp(n), map[string]any{"model": "claude/b"}) },
				remove: func() *http.Response { return asUser(b, s, "DELETE", "/api/profiles/claude", nil) },
				kind:   "model", id: "claude/b",
				writeOK: http.StatusOK, removeOK: http.StatusNoContent,
			}
		},
	}
}

// answer is a response read to the end.
type answer struct {
	status int
	code   string
	raw    string
}

func async(f func() *http.Response) <-chan answer {
	out := make(chan answer, 1)
	go func() {
		resp := f()
		raw, _ := io.ReadAll(resp.Body)
		var env errorEnvelope
		_ = json.Unmarshal(raw, &env)
		out <- answer{resp.StatusCode, env.Error.Code, string(raw)}
	}()
	return out
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	v := <-ch
	return v
}

// waitingSeam returns a channel closed once someone waits for the refs lock.
func waitingSeam(s *Server) <-chan struct{} {
	ch := make(chan struct{})
	var once sync.Once
	f := func() { once.Do(func() { close(ch) }) }
	s.refs.waiting.Store(&f)
	return ch
}

func clearRefsSeams(s *Server) {
	s.refs.waiting.Store(nil)
	s.refs.looked.Store(nil)
	s.refs.removing.Store(nil)
}

// TestTicketWriteBeforeRemoval stops a ticket write after it found its
// project or model, starts the removal, and lets the write go on only once
// the removal waits: the ticket is written and the removal is refused.
func TestTicketWriteBeforeRemoval(t *testing.T) {
	for name, build := range refsRaces(t) {
		t.Run(name, func(t *testing.T) {
			r := build(t)
			defer clearRefsSeams(r.s)
			stopped, release := make(chan struct{}), make(chan struct{})
			// Released on every way out, so that no request stays stopped.
			releaseAll := sync.OnceFunc(func() { close(release) })
			defer releaseAll()
			var once sync.Once
			look := func(kind, id string) {
				if kind != r.kind || id != r.id {
					return
				}
				once.Do(func() {
					if r.missing != "" {
						if err := os.RemoveAll(r.missing); err != nil {
							t.Error(err)
						}
					}
					close(stopped)
					<-release
				})
			}
			r.s.refs.looked.Store(&look)
			written := async(r.write)
			receive(t, stopped, "the ticket write to stop")
			waiting := waitingSeam(r.s)
			removed := async(r.remove)
			select {
			case <-waiting:
			case a := <-removed:
				releaseAll()
				t.Fatalf("the removal did not wait for the ticket write: %d %s", a.status, a.raw)

			}
			releaseAll()
			if a := receive(t, written, "the ticket write"); a.status != r.writeOK {
				t.Fatalf("ticket write: %d %s", a.status, a.raw)
			}
			if a := receive(t, removed, "the removal"); a.status != http.StatusConflict || a.code != api.CodeInUse {
				t.Fatalf("removal: %d %s, want 409 in_use", a.status, a.raw)
			}
		})
	}
}

// TestRemovalBeforeTicketWrite stops a removal after its in-use check,
// starts the ticket write, and lets the removal go on only once the write
// waits: the removal is written and the ticket write is refused.
func TestRemovalBeforeTicketWrite(t *testing.T) {
	for name, build := range refsRaces(t) {
		t.Run(name, func(t *testing.T) {
			r := build(t)
			defer clearRefsSeams(r.s)
			if r.missing != "" {
				if err := os.RemoveAll(r.missing); err != nil {
					t.Fatal(err)
				}
			}
			stopped, release := make(chan struct{}), make(chan struct{})
			// Released on every way out, so that no request stays stopped.
			releaseAll := sync.OnceFunc(func() { close(release) })
			defer releaseAll()
			var once sync.Once
			hold := func() { once.Do(func() { close(stopped); <-release }) }
			r.s.refs.removing.Store(&hold)
			removed := async(r.remove)
			receive(t, stopped, "the removal to stop")
			waiting := waitingSeam(r.s)
			written := async(r.write)
			select {
			case <-waiting:
			case a := <-written:
				releaseAll()
				t.Fatalf("the ticket write did not wait for the removal: %d %s", a.status, a.raw)

			}
			releaseAll()
			if a := receive(t, removed, "the removal"); a.status != r.removeOK {
				t.Fatalf("removal: %d %s", a.status, a.raw)
			}
			if a := receive(t, written, "the ticket write"); a.status != http.StatusUnprocessableEntity || a.code != api.CodeValidation {
				t.Fatalf("ticket write: %d %s, want 422 validation", a.status, a.raw)
			}
		})
	}
}
