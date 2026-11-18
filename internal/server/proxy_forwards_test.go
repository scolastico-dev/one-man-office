package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func TestProxyForwardsRoutes(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.PublicURL = "https://office.example" })
	ctx := context.Background()
	user := apiclient.NewWithTransport(apiclient.HandlerTransport(s.SocketHandler()), "")
	empty, err := user.ProxyForwards(ctx, nil)
	if err != nil || empty.Forwards == nil || len(empty.Forwards) != 0 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	if strings.TrimSpace(string(user.LastBody())) != `{"forwards":[]}` {
		t.Fatalf("empty JSON %s", user.LastBody())
	}
	dev := liveSession(t, s, api.RoleDeveloper)
	client := apiclient.NewWithTransport(apiclient.HandlerTransport(s.SocketHandler()), dev.ID)
	f, err := client.CreateProxyForward(ctx, api.CreateProxyForward{Ticket: dev.Ticket, Port: 5173, Name: "preview", Host: "::1", StartPath: "/demo?q=hello%20world"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Host != "::1" || f.Ticket == nil || *f.Ticket != *dev.Ticket || f.Address != "https://office.example"+f.BasePath+"demo?q=hello%20world" {
		t.Fatalf("created: %+v", f)
	}
	list, err := client.ProxyForwards(ctx, dev.Ticket)
	if err != nil || len(list.Forwards) != 1 || list.Forwards[0].ID != f.ID || list.Forwards[0].Address != f.Address {
		t.Fatalf("listed %+v %v", list, err)
	}
	global, err := user.CreateProxyForward(ctx, api.CreateProxyForward{Port: 3000, Name: "global"})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []api.Role{api.RoleDeveloper, api.RoleReviewer, api.RolePM, api.RoleCEO} {
		sess := liveSession(t, s, role)
		rec := socketDo(t, s, "POST", "/api/proxy/forwards", api.CreateProxyForward{Ticket: dev.Ticket, Port: 1, Name: "other"}, sess.ID)
		wantStatus(t, rec.Code, http.StatusForbidden)
		rec = socketDo(t, s, "POST", "/api/proxy/forwards", api.CreateProxyForward{Port: 1, Name: "global"}, sess.ID)
		wantStatus(t, rec.Code, http.StatusForbidden)
		rec = socketDo(t, s, "DELETE", "/api/proxy/forwards/"+global.ID, nil, sess.ID)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Fatalf("global delete %s: %d %s", role, rec.Code, rec.Body)
		}
		if sess.Ticket != nil {
			rec = socketDo(t, s, "POST", "/api/proxy/forwards", api.CreateProxyForward{Ticket: sess.Ticket, Port: 1, Name: "own"}, sess.ID)
			wantStatus(t, rec.Code, http.StatusCreated)
			own := decodeBody[api.ProxyForward](t, rec.Body)
			rec = socketDo(t, s, "DELETE", "/api/proxy/forwards/"+own.ID, nil, sess.ID)
			wantStatus(t, rec.Code, http.StatusNoContent)
		}
	}
	other := liveSession(t, s, api.RoleDeveloper)
	for _, n := range []int{*other.Ticket, 999999} {
		rec := socketDo(t, s, "GET", "/api/proxy/forwards?ticket="+strconv.Itoa(n), nil, dev.ID)
		wantStatus(t, rec.Code, http.StatusForbidden)
	}
	rec := socketDo(t, s, "DELETE", "/api/proxy/forwards/"+f.ID, nil, other.ID)
	wantStatus(t, rec.Code, http.StatusNotFound)
	list, err = client.ProxyForwards(ctx, nil)
	if err != nil || len(list.Forwards) != 1 || list.Forwards[0].ID != f.ID {
		t.Fatalf("scoped list: %+v %v", list, err)
	}
	if err := client.DeleteProxyForward(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := user.DeleteProxyForward(ctx, global.ID); err != nil {
		t.Fatal(err)
	}
	empty, err = user.ProxyForwards(ctx, nil)
	if err != nil || empty.Forwards == nil || len(empty.Forwards) != 0 {
		t.Fatalf("after delete: %+v %v", empty, err)
	}
	for _, query := range []string{"ticket=0", "ticket=-1", "ticket=no", "ticket=1&ticket=2", "ticket="} {
		rec := socketDo(t, s, "GET", "/api/proxy/forwards?"+query, nil, "")
		wantStatus(t, rec.Code, http.StatusBadRequest)
	}
	for _, body := range []any{map[string]any{"port": 1, "name": "dev", "unknown": true}, map[string]any{"ticket": 1, "port": "bad", "name": "dev"}} {
		rec := socketDo(t, s, "POST", "/api/proxy/forwards", body, "")
		wantStatus(t, rec.Code, http.StatusBadRequest)
	}
}

func TestProxyForwardsBrowserAuthAndRouteTable(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	for _, row := range []struct {
		pattern string
		access  routeAccess
	}{
		{"GET /api/proxy/forwards", agentRead}, {"POST /api/proxy/forwards", agentWrite}, {"DELETE /api/proxy/forwards/{id}", agentWrite},
	} {
		found := false
		for _, got := range s.routeTable {
			if got.pattern == row.pattern {
				found = true
				if got.access != row.access {
					t.Errorf("%s: %s", row.pattern, got.access)
				}
			}
		}
		if !found {
			t.Errorf("route missing: %s", row.pattern)
		}
	}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		path := "/api/proxy/forwards"
		if method == "DELETE" {
			path += "/missing"
		}
		resp := b.do(method, path, map[string]any{}, nil)
		wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
	}
	resp := b.do("POST", "/api/proxy/forwards", api.CreateProxyForward{Port: 5173, Name: "dev"}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	f := decodeBody[api.ProxyForward](t, resp.Body)
	resp = b.do("POST", "/api/proxy/forwards", api.CreateProxyForward{Port: 5173, Name: "dev"}, map[string]string{"Authorization": "Bearer " + s.token, "Content-Type": "text/plain"})
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	resp = b.do("DELETE", "/api/proxy/forwards/"+f.ID, nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusNoContent)
}
