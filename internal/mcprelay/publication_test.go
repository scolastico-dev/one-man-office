package mcprelay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
	"golang.org/x/oauth2"
)

// The status event must be recorded before acquire releases its callers,
// including when a start fails. Status itself must already describe the outcome.
func TestStartupPublicationPrecedesReady(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			s := stdioServer("fake", nil)
			if fail {
				s.Command = t.TempDir() + "/missing"
			}
			f := newFixture(t, nil, s)
			// Capture the instance before its start (the missing-command failure
			// removes it from the pool before announcing its outcome).
			p := f.pool
			p.mu.Lock()
			inst := newInstance(p, s, "s", f.home, p.state(s.Name))
			p.instances[instanceKey{s.Name, "s"}] = inst
			p.mu.Unlock()
			p.o.Emit = func(kind string, data any) {
				if kind == api.KindMCPChanged {
					select {
					case <-inst.ready:
						t.Error("startup released callers before publishing its status")
					default:
					}
					st := data.(api.MCPServerState)
					if !fail && st.State != api.MCPStateRunning {
						t.Errorf("startup status %s", st.State)
					}
					if fail && st.LastError == "" {
						t.Error("missing startup error")
					}
				}
				f.events.emit(kind, data)
			}
			go inst.start()
			<-inst.ready
			err := inst.err
			if !fail && err != nil {
				t.Fatal(err)
			}
			if fail && err == nil {
				t.Fatal("missing startup failure")
			}
			if f.events.count(api.KindMCPChanged) != 1 {
				t.Error("ready closed before startup publication completed")
			}
		})
	}
}

// A queued flag cleared before the RPC or its publication ends is not an
// idle barrier. Notices during that RPC must cause one subsequent listing.
func TestRelistTracksWorkThroughPublication(t *testing.T) {
	testControlledRelist(t, true)
}

// A flood without a real change must neither advance the generation nor
// emit status/tools events. Block a request to define the burst explicitly.
// (stdio_test.go holds the same rule over a real stdio server, where the
// burst is only bounded by the re-listing's end.)
func TestControlledBurstWithoutAChangeAnnouncesNothing(t *testing.T) {
	testControlledRelist(t, false)
}

func testControlledRelist(t *testing.T, change bool) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	publishing, publicationRelease := make(chan struct{}), make(chan struct{})
	var publicationOnce, publicationReleaseOnce sync.Once
	releasePublication := func() { publicationReleaseOnce.Do(func() { close(publicationRelease) }) }
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var mu sync.Mutex
	lists := 0
	upstream := mcp.NewServer(&mcp.Implementation{Name: "controlled", Version: "1"}, nil)
	upstream.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, req)
			}
			mu.Lock()
			lists++
			n := lists
			mu.Unlock()
			if n == 2 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			typ := "string"
			if n >= 2 && change {
				typ = "integer"
			}
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{
				"type": "object", "properties": map[string]any{"value": map[string]any{"type": typ}},
			}}}}, nil
		}
	})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return upstream }, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true}))
	defer srv.Close()
	f := newFixture(t, nil, httpServer("remote", srv.URL))
	f.pool.o.Emit = func(kind string, data any) {
		if change && kind == api.KindMCPChanged && f.pool.Generation() == 2 {
			publicationOnce.Do(func() { close(publishing); <-publicationRelease })
		}
		f.events.emit(kind, data)
		if change && kind == api.KindMCPChanged && f.events.count(kind) == 2 && f.pool.Generation() != 2 {
			t.Error("the new list was announced before its generation advanced")
		}
	}
	defer f.pool.Close()
	defer unblock()
	defer releasePublication()
	id := agent("s", f.home, api.RoleDeveloper)
	if _, err := f.pool.Tools(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	f.pool.mu.Lock()
	inst := f.pool.instances[instanceKey{"remote", ""}]
	f.pool.mu.Unlock()
	gen := f.pool.Generation()
	inst.toolListChanged()
	<-entered
	inst.mu.Lock()
	queued := inst.relistQueued
	inst.mu.Unlock()
	if !queued {
		t.Error("in-flight relist is reported as idle")
	}
	for range 50 {
		inst.toolListChanged()
	}
	unblock()
	if change {
		<-publishing
		inst.mu.Lock()
		active, done := inst.relistQueued, inst.relistDone
		inst.mu.Unlock()
		if !active {
			t.Error("relist became idle before publication finished")
		}
		select {
		case <-done:
			t.Error("relist completion closed during publication")
		default:
		}
		releasePublication()
	}
	eventually(t, "all relists and publication", func() bool {
		inst.mu.Lock()
		defer inst.mu.Unlock()
		return !inst.relistQueued && inst.relists >= 2
	})
	mu.Lock()
	got := lists
	mu.Unlock()
	if got != 3 {
		t.Errorf("got %d listings including startup; want 3", got)
	}
	wantGen, wantEvents := gen, 1
	if change {
		wantGen++
		wantEvents++
	}
	if f.pool.Generation() != wantGen || f.events.count(api.KindMCPToolsChanged) != wantEvents || f.events.count(api.KindMCPChanged) != wantEvents {
		t.Errorf("change=%v: generation %d -> %d (want %d), events:\n%s", change, gen, f.pool.Generation(), wantGen, f.events.all())
	}
	if change {
		tools := inst.toolList()
		schema := tools[0].Tool.InputSchema.(map[string]any)
		if schema["properties"].(map[string]any)["value"].(map[string]any)["type"] != "integer" {
			t.Errorf("schema update was not published: %v", schema)
		}
	}
}

// Ending an instance drains pending relist work instead of leaving a worker
// sleeping until its gap (or issuing another request after removal).
func TestStopDrainsPendingRelist(t *testing.T) {
	srv := httptest.NewServer(fakemcp.Handler(fakemcp.Config{}))
	defer srv.Close()
	f := newFixture(t, func(t *tunables) { t.relistGap = time.Hour }, httpServer("remote", srv.URL))
	if _, err := f.pool.Tools(context.Background(), agent("s", f.home, api.RoleDeveloper)); err != nil {
		t.Fatal(err)
	}
	f.pool.mu.Lock()
	inst := f.pool.instances[instanceKey{"remote", ""}]
	f.pool.mu.Unlock()
	inst.mu.Lock()
	inst.lastRelist = time.Now()
	inst.mu.Unlock()
	inst.toolListChanged()
	done := make(chan struct{})
	go func() { f.pool.Restart("remote"); close(done) }()
	<-done
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.relistQueued || inst.relists != 0 {
		t.Fatalf("pending relist survived stop: queued=%v completed=%d", inst.relistQueued, inst.relists)
	}
}

func TestLostSignInPublishesStatusOnce(t *testing.T) {
	as := newFakeAS(t)
	f := newFixture(t, nil, oauthServer("remote", as.url()))
	f.signIn("remote")
	if res := f.call(agent("s", f.home, api.RoleDeveloper), "remote__echo", nil); res.IsError {
		t.Fatal(resultText(res))
	}
	before := f.events.count(api.KindMCPChanged)
	gen := f.pool.Generation()
	stored, _, _ := f.store.Load(context.Background(), "remote")
	f.pool.markNeedsLogin(stored, nil, "the server refused omo's sign-in")
	if got := f.events.count(api.KindMCPChanged) - before; got != 1 {
		t.Fatalf("published status %d times for one lost sign-in", got)
	}
	if f.pool.Generation() != gen+1 || f.state("remote").State != api.MCPStateNeedsLogin {
		t.Fatal("lost sign-in was not published")
	}
}

// A slow listing can finish after reset removed its instance. It must not
// replace the last published tools or announce a retired instance's result.
func TestRemovedInstanceCannotPublishRelist(t *testing.T) {
	testRetiredRelist(t, false)
}

func TestRestartCancelsInflightRelist(t *testing.T) {
	testRetiredRelist(t, true)
}

func testRetiredRelist(t *testing.T, restart bool) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var mu sync.Mutex
	lists := 0
	upstream := mcp.NewServer(&mcp.Implementation{Name: "controlled", Version: "1"}, nil)
	upstream.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, req)
			}
			mu.Lock()
			lists++
			n := lists
			mu.Unlock()
			name := "initial"
			if n > 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				name = "retired"
			}
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: name, InputSchema: map[string]any{"type": "object"}}}}, nil
		}
	})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return upstream }, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true}))
	defer srv.Close()
	f := newFixture(t, nil, httpServer("remote", srv.URL))
	defer f.pool.Close()
	if _, err := f.pool.Tools(context.Background(), agent("s", f.home, api.RoleDeveloper)); err != nil {
		t.Fatal(err)
	}
	f.pool.mu.Lock()
	inst := f.pool.instances[instanceKey{"remote", ""}]
	f.pool.mu.Unlock()
	defer inst.stop("test ended")
	defer unblock()
	gen := f.pool.Generation()
	inst.toolListChanged()
	<-entered
	wantEvents := 1
	if restart {
		restarted := make(chan error, 1)
		go func() { restarted <- f.pool.Restart("remote") }()
		if err := <-restarted; err != nil {
			t.Fatal(err)
		}
		wantEvents = 2
	} else {
		// This is reset's removal boundary; leave its RPC alive to force the
		// response after removal, regardless of transport cancellation.
		f.pool.mu.Lock()
		f.pool.removeInstance(inst)
		f.pool.mu.Unlock()
	}
	unblock()
	inst.mu.Lock()
	done := inst.relistDone
	inst.mu.Unlock()
	<-done
	if f.pool.Generation() != gen || f.events.count(api.KindMCPChanged) != wantEvents || f.events.count(api.KindMCPToolsChanged) != 1 {
		t.Fatalf("retired instance published a change:\n%s", f.events.all())
	}
	if got := f.state("remote").Tools; len(got) != 1 || got[0] != "remote__initial" {
		t.Fatalf("retired list replaced server status: %v", got)
	}
	if got := toolNames(inst.toolList()); len(got) != 1 || got[0] != "remote__initial" {
		t.Fatalf("retired list replaced instance tools: %v", got)
	}
}

func TestRemovedStartupCannotPublish(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			upstream := mcp.NewServer(&mcp.Implementation{Name: "controlled", Version: "1"}, nil)
			upstream.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
				return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
					if method != "tools/list" {
						return next(ctx, method, req)
					}
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					if fail {
						return nil, errors.New("retired start failed")
					}
					return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "retired", InputSchema: map[string]any{"type": "object"}}}}, nil
				}
			})
			srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return upstream }, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true}))
			defer srv.Close()
			cfg := httpServer("remote", srv.URL)
			f := newFixture(t, nil, cfg)
			f.pool.mu.Lock()
			inst := newInstance(f.pool, cfg, "", f.home, f.pool.state("remote"))
			f.pool.instances[instanceKey{"remote", ""}] = inst
			f.pool.mu.Unlock()
			defer inst.stop("test ended")
			defer unblock()
			go inst.start()
			<-entered
			f.pool.mu.Lock()
			f.pool.removeInstance(inst)
			f.pool.mu.Unlock()
			unblock()
			<-inst.ready
			f.pool.mu.Lock()
			failures := len(inst.st.failures)
			f.pool.mu.Unlock()
			if failures != 0 || f.pool.Generation() != 0 || f.events.all() != "" || len(f.state("remote").Tools) != 0 {
				t.Fatalf("retired startup changed shared state: failures=%d generation=%d events=%s", failures, f.pool.Generation(), f.events.all())
			}
		})
	}
}

func TestRemovedWatcherCannotRecordFailure(t *testing.T) {
	srv := httptest.NewServer(fakemcp.Handler(fakemcp.Config{}))
	defer srv.Close()
	cfg := httpServer("remote", srv.URL)
	f := newFixture(t, nil, cfg)
	f.pool.mu.Lock()
	inst := newInstance(f.pool, cfg, "", f.home, f.pool.state("remote"))
	f.pool.instances[instanceKey{"remote", ""}] = inst
	f.pool.mu.Unlock()
	// Connect without start's automatic watcher, so this test can join watch.
	if err := inst.connect(inst.connCtx); err != nil {
		t.Fatal(err)
	}
	close(inst.ready)
	defer inst.stop("test ended")
	done := make(chan struct{})
	go func() { inst.watch(); close(done) }()
	f.pool.mu.Lock()
	inst.session.Close()
	// watch has read stopping=false, then sets it true before taking pool.mu.
	eventually(t, "watch to observe the ended connection", func() bool { inst.mu.Lock(); defer inst.mu.Unlock(); return inst.stopping })
	f.pool.removeInstance(inst)
	f.pool.mu.Unlock()
	<-done
	f.pool.mu.Lock()
	failures := len(inst.st.failures)
	f.pool.mu.Unlock()
	if failures != 0 || f.events.all() != "" {
		t.Fatalf("retired watcher published failure: failures=%d events=%s", failures, f.events.all())
	}
}

func TestRetiredOAuthCallbacksKeepNewSignIn(t *testing.T) {
	for _, callback := range []string{"http refusal", "refresh refusal", "refreshed token"} {
		t.Run(callback, func(t *testing.T) {
			as := newFakeAS(t)
			f := newFixture(t, nil, oauthServer("remote", as.url()))
			f.signIn("remote")
			if res := f.call(agent("s", f.home, api.RoleDeveloper), "remote__echo", nil); res.IsError {
				t.Fatal(resultText(res))
			}
			f.pool.mu.Lock()
			inst := f.pool.instances[instanceKey{"remote", ""}]
			f.pool.mu.Unlock()
			source := inst.ts
			f.signIn("remote")
			token := f.storedToken("remote")
			gen := f.pool.Generation()
			events := f.events.all()
			if callback == "http refusal" {
				f.pool.loginLost(inst, source.snapshot())
			} else if callback == "refresh refusal" {
				f.pool.markNeedsLogin(source.snapshot(), nil, "the authorization server refused the retired refresh")
			} else {
				// A token refresh which started under the previous sign-in finishes
				// after the replacement has been saved.
				source.src = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "retired-refreshed", RefreshToken: "retired-refresh"})
				if _, err := source.Token(); err != nil {
					t.Fatal(err)
				}
			}
			if f.storedToken("remote") != token || f.state("remote").Auth.State != api.MCPAuthStateConnected || f.pool.Generation() != gen || f.events.all() != events {
				t.Fatalf("retired OAuth callback changed the new sign-in; events:\n%s", f.events.all())
			}
		})
	}
}

func TestStartupRefusalKeepsReplacementCredential(t *testing.T) {
	as := newFakeAS(t)
	f := newFixture(t, nil, oauthServer("remote", as.url()))
	f.signIn("remote")
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	as.mcp = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		http.Error(w, "old credential refused", http.StatusUnauthorized)
	})
	toolsBefore := f.events.count(api.KindMCPToolsChanged)
	done := make(chan struct{})
	go func() { f.pool.Tools(context.Background(), agent("s", f.home, api.RoleDeveloper)); close(done) }()
	<-entered
	// saveLogin stores the replacement before loginFinished resets instances.
	f.pool.mu.Lock()
	replacement, _, err := f.store.Load(context.Background(), "remote")
	if err == nil {
		replacement.Token = &oauth2.Token{AccessToken: "replacement-access", RefreshToken: "replacement-refresh", Expiry: time.Now().Add(time.Hour)}
		err = f.store.Save(context.Background(), replacement)
	}
	f.pool.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	unblock()
	<-done
	if f.storedToken("remote") != "replacement-access" || f.state("remote").Auth.State != api.MCPAuthStateConnected {
		t.Fatalf("old startup refusal invalidated replacement credential: %+v", f.state("remote").Auth)
	}
	if f.events.count(api.KindMCPToolsChanged) != toolsBefore {
		t.Fatalf("old startup refusal announced a credential loss:\n%s", f.events.all())
	}
}

func TestExpiredCredentialStartupPublishesOnce(t *testing.T) {
	as := newFakeAS(t)
	as.expiresIn = 1
	cfg := oauthServer("remote", as.url())
	f := newFixture(t, nil, cfg)
	f.signIn("remote")
	as.revokeAll()
	before := f.events.count(api.KindMCPChanged)
	gen := f.pool.Generation()
	p := f.pool
	p.mu.Lock()
	inst := newInstance(p, cfg, "", f.home, p.state("remote"))
	p.instances[instanceKey{"remote", ""}] = inst
	p.mu.Unlock()
	p.o.Emit = func(kind string, data any) {
		if kind == api.KindMCPChanged {
			select {
			case <-inst.ready:
				t.Error("expired startup published after releasing callers")
			default:
			}
		}
		f.events.emit(kind, data)
	}
	go inst.start()
	<-inst.ready
	if got := f.events.count(api.KindMCPChanged) - before; got != 1 || f.pool.Generation() != gen+1 {
		t.Fatalf("expired startup publication: statuses=%d generations=%d", got, f.pool.Generation()-gen)
	}
	if f.storedToken("remote") != "" || f.state("remote").Auth.State != api.MCPAuthStateNeedsLogin {
		t.Fatal("expired startup did not require sign-in")
	}
}
