package board

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func addForward(t *testing.T, b *Board, ticket *int) api.ProxyForward {
	t.Helper()
	f, err := b.CreateProxyForward(context.Background(), User, api.CreateProxyForward{Ticket: ticket, Port: 5173, Name: "dev server"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestProxyForwardDefaultsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	env := newFakeEnv()
	b := New(d, env, testOptions(env))
	f := addForward(t, b, nil)
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`).MatchString(f.ID) || f.Host != "127.0.0.1" || f.Scheme != "http" || f.StartPath != "/" || f.StripPrefix || f.VerifyTLS || f.Ticket != nil || f.BasePath != "/proxy/"+f.ID+"/" || f.Address != f.BasePath {
		t.Fatalf("defaults: %+v", f)
	}
	second := addForward(t, b, nil)
	if f.ID == second.ID {
		t.Fatal("ids reused")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	b = New(d, env, testOptions(env))
	got, err := b.LiveProxyForward(context.Background(), System, f.ID)
	if err != nil || !reflect.DeepEqual(got, f) {
		t.Fatalf("reopened: %+v %v", got, err)
	}
}

func TestProxyForwardValidation(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	fullHost := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + "."
	for _, host := range []string{"127.0.0.1", "example.com", "my_service", "192.0.2.4", "::1", "[::1]", "fe80::1%eth0", "cafe\u0301.example", fullHost} {
		f, err := b.CreateProxyForward(ctx, User, api.CreateProxyForward{Host: host, Port: 65535, Scheme: "https", Name: "UI <preview>", StartPath: "/a/b?x=1&next=https%3A%2F%2Fexample.com", StripPrefix: true, VerifyTLS: true})
		if err != nil || !f.StripPrefix || !f.VerifyTLS || f.Address != f.BasePath+"a/b?x=1&next=https%3A%2F%2Fexample.com" {
			t.Errorf("host %q: %+v %v", host, f, err)
		}
	}
	cases := map[string]func(*api.CreateProxyForward){
		"no port":       func(r *api.CreateProxyForward) { r.Port = 0 },
		"negative port": func(r *api.CreateProxyForward) { r.Port = -1 },
		"large port":    func(r *api.CreateProxyForward) { r.Port = 65536 },
		"no name":       func(r *api.CreateProxyForward) { r.Name = "  " },
		"newline name":  func(r *api.CreateProxyForward) { r.Name = "x\ny" },
		"escape name":   func(r *api.CreateProxyForward) { r.Name = "x\x1b[2J" },
		"scheme":        func(r *api.CreateProxyForward) { r.Scheme = "file" },
	}
	for _, host := range []string{"http://localhost", "localhost:80", "x/y", "user@host", "x?y", "x#y", "x y", "x\ny", "[bad]", "-bad", "bad..host"} {
		cases["host "+host] = func(r *api.CreateProxyForward) { r.Host = host }
	}
	for _, path := range []string{"http://example.com/", "//host/x", "relative", "/../x", "/a/../../x", "/%2e%2e/x", "/a/%2e%2e/%2e%2e/x", "/%252e%252e/x", "/%25x/%252e%252e/%252e%252e/x", "/%25x/%250d", "/%2f../x", "/\\host", "/x\n", "/%0d%0a", "/?q=%00", "/bad%xx", "/x#fragment"} {
		cases["path "+path] = func(r *api.CreateProxyForward) { r.StartPath = path }
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			req := api.CreateProxyForward{Port: 5173, Name: "dev"}
			change(&req)
			_, err := b.CreateProxyForward(ctx, User, req)
			wantCode(t, err, api.CodeValidation)
		})
	}
	for _, path := range []string{"/", "/a/../b", "/a%20b?q=hello+world&x=%2F", "/?next=//example.com"} {
		_, err := b.CreateProxyForward(ctx, User, api.CreateProxyForward{Port: 1, Name: "dev", StartPath: path})
		if err != nil {
			t.Errorf("valid path %q: %v", path, err)
		}
	}
}

func TestProxyForwardScopes(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()
	records := map[string]api.ProxyForward{}
	global := addForward(t, f.b, nil)
	for _, rel := range scopeRelations {
		records[rel] = addForward(t, f.b, intPtr(f.ticket[rel]))
	}
	for name, a := range f.actor {
		t.Run(name, func(t *testing.T) {
			got, err := f.b.ProxyForwards(ctx, a, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{}
			_, _, all := readScope(a)
			if all {
				want = append(want, global.ID)
			}
			for _, rel := range scopeRelations {
				id, n := records[rel].ID, f.ticket[rel]
				_, err = f.b.ProxyForward(ctx, a, id)
				filtered, ferr := f.b.ProxyForwards(ctx, a, &n)
				if wantRead[name][rel] {
					want = append(want, id)
					if err != nil || ferr != nil || len(filtered) != 1 {
						t.Errorf("read %s: %v %v %+v", rel, err, ferr, filtered)
					}
				} else {
					wantCode(t, err, api.CodeNotFound)
					wantCode(t, ferr, api.CodeForbidden)
				}
				req := api.CreateProxyForward{Ticket: &n, Port: 1, Name: "own"}
				added, err := f.b.CreateProxyForward(ctx, a, req)
				allowed := a.Role == api.RoleUser || a.Ticket == int64(n)
				if allowed {
					if err != nil {
						t.Fatal(err)
					}
					if err = f.b.DeleteProxyForward(ctx, a, added.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					wantCode(t, err, api.CodeForbidden)
					if err = f.b.DeleteProxyForward(ctx, a, id); errCode(err) != api.CodeForbidden && errCode(err) != api.CodeNotFound {
						t.Errorf("delete %s: %v", rel, err)
					}
				}
			}
			ids := []string{}
			for _, v := range got {
				ids = append(ids, v.ID)
			}
			slices.Sort(ids)
			slices.Sort(want)
			if !slices.Equal(ids, want) {
				t.Errorf("list %v want %v", ids, want)
			}
			if a.Role != api.RoleUser {
				_, err = f.b.CreateProxyForward(ctx, a, api.CreateProxyForward{Port: 1, Name: "global"})
				wantCode(t, err, api.CodeForbidden)
				err = f.b.DeleteProxyForward(ctx, a, global.ID)
				if errCode(err) != api.CodeForbidden && errCode(err) != api.CodeNotFound {
					t.Errorf("global delete: %v", err)
				}
			}
			_, err = f.b.LiveProxyForward(ctx, a, global.ID)
			wantCode(t, err, api.CodeForbidden)
		})
	}
	_, err := f.b.ProxyForwards(ctx, f.actor["developer"], intPtr(999))
	wantCode(t, err, api.CodeForbidden)
	_, err = f.b.CreateProxyForward(ctx, f.actor["developer"], api.CreateProxyForward{Ticket: intPtr(999), Port: 1, Name: "missing"})
	wantCode(t, err, api.CodeForbidden)
}

func TestProxyForwardCleanup(t *testing.T) {
	ctx := context.Background()
	for _, close := range []string{"merge", "export", "finish export", "nothing to export", "no project review", "skipped review", "cancel", "cancel story"} {
		t.Run(close, func(t *testing.T) {
			b, _ := newTestBoard(t)
			req := api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}}
			if close == "no project review" || close == "skipped review" {
				req.Projects = nil
				req.Review = api.ReviewHuman
			}
			if close == "skipped review" {
				req.Review = api.ReviewSkipped
			}
			if close == "cancel story" {
				req.Type = api.TypeStory
			}
			tk := mustCreate(t, b, User, req)
			owned := []api.ProxyForward{addForward(t, b, &tk.Number), addForward(t, b, &tk.Number)}
			global := addForward(t, b, nil)
			other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "other"})
			keep := addForward(t, b, &other.Number)
			if close == "cancel story" {
				for _, col := range []api.Column{api.ColumnPlanning, api.ColumnReady, api.ColumnInProgress, api.ColumnReview} {
					task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "child", Story: &tk.Number, Projects: []string{"api"}})
					force(t, b, task.Number, col, "")
					owned = append(owned, addForward(t, b, &task.Number))
				}
			}
			start := lastEvent(t, b)
			var err error
			switch close {
			case "merge":
				force(t, b, tk.Number, api.ColumnReview, api.ReviewStageMerging)
				err = b.CompleteMerge(ctx, int64(tk.Number))
			case "export":
				force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
				err = b.CompleteExport(ctx, int64(tk.Number), "https://example.com/pr/1")
			case "finish export", "nothing to export":
				force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
				if close == "finish export" {
					_, err = b.db.Exec(`UPDATE ticket_projects SET export_url='https://example.com/pr/1' WHERE ticket_id=?`, tk.Number)
					if err != nil {
						t.Fatal(err)
					}
				}
				err = b.FinishExport(ctx, int64(tk.Number))
			case "no project review":
				force(t, b, tk.Number, api.ColumnReview, api.ReviewStageHuman)
				_, err = b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{})
			case "skipped review":
				force(t, b, tk.Number, api.ColumnInProgress, "")
				dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
				_, err = b.Done(ctx, dev, api.DoneRequest{})
			default:
				_, err = b.Cancel(ctx, User, int64(tk.Number), api.CancelRequest{})
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range owned {
				_, err = b.LiveProxyForward(ctx, System, f.ID)
				wantCode(t, err, api.CodeNotFound)
			}
			for _, f := range []api.ProxyForward{global, keep} {
				if _, err = b.LiveProxyForward(ctx, System, f.ID); err != nil {
					t.Fatal(err)
				}
			}
			removed := map[string]bool{}
			for _, ev := range eventsAfter(t, b, start) {
				if ev.Kind == api.KindProxyForwardRemoved {
					var data api.ProxyForward
					if err := json.Unmarshal([]byte(ev.Detail), &data); err != nil {
						t.Fatal(err)
					}
					removed[data.ID] = true
					if data.Ticket == nil {
						t.Fatal("cleanup removed global")
					}
				}
			}
			if len(removed) != len(owned) {
				t.Errorf("removed events: %v want %d", removed, len(owned))
			}
			_, err = b.CreateProxyForward(ctx, User, api.CreateProxyForward{Ticket: &tk.Number, Port: 1, Name: "closed"})
			wantCode(t, err, api.CodeInvalidTransition)
		})
	}
}

func TestProxyForwardCleanupRollback(t *testing.T) {
	b, _ := newTestBoard(t)
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T"})
	f := addForward(t, b, &tk.Number)
	if _, err := b.db.Exec(`CREATE TRIGGER refuse_removal BEFORE INSERT ON events WHEN NEW.kind='proxy.forward_removed' BEGIN SELECT RAISE(ABORT,'refused'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := b.Cancel(context.Background(), User, int64(tk.Number), api.CancelRequest{})
	if err == nil {
		t.Fatal("cancel succeeded despite failed event")
	}
	if get(t, b, tk.Number).Column == api.ColumnDone {
		t.Fatal("Done committed without cleanup event")
	}
	if _, err := b.LiveProxyForward(context.Background(), System, f.ID); err != nil {
		t.Fatal(err)
	}
}

func TestProxyForwardChangeEvents(t *testing.T) {
	b, _ := newTestBoard(t)
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T"})
	ctx := context.Background()
	for _, ticket := range []*int{nil, &tk.Number} {
		start := lastEvent(t, b)
		f := addForward(t, b, ticket)
		if err := b.DeleteProxyForward(ctx, User, f.ID); err != nil {
			t.Fatal(err)
		}
		events := eventsAfter(t, b, start)
		if len(events) != 2 {
			t.Fatalf("events: %+v", events)
		}
		for i, kind := range []string{api.KindProxyForwardAdded, api.KindProxyForwardRemoved} {
			var record api.ProxyForward
			if err := json.Unmarshal([]byte(events[i].Detail), &record); err != nil {
				t.Fatal(err)
			}
			if events[i].Kind != kind || !reflect.DeepEqual(record, f) {
				t.Fatalf("event %s: %+v want %+v", events[i].Kind, record, f)
			}
		}
		_, err := b.ProxyForward(ctx, User, f.ID)
		wantCode(t, err, api.CodeNotFound)
	}
}
