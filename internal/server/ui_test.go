package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

type uiFeedContract struct {
	Notifications []struct {
		ID          string `json:"id"`
		Kind        string `json:"kind"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		Destination string `json:"destination"`
	} `json:"notifications"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

func TestUICapabilityExchange(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	for _, request := range []map[string]any{{"features": []string{"ui.notifications.system.v1", "future.unknown"}}, {}, {"features": []string{}}} {
		got := okJSON[struct {
			Features []string `json:"features"`
		}](t, u, 200, "POST", "/api/ui/capabilities", request)
		if !slices.Contains(got.Features, "ui.capabilities.v1") || !slices.Contains(got.Features, "ui.notifications.poll.v1") || slices.Contains(got.Features, "future.unknown") {
			t.Fatalf("server features: %+v", got)
		}
	}
	u.fails(400, api.CodeBadRequest, "POST", "/api/ui/capabilities", map[string]any{"features": "not a list"})
}

func TestUINotificationFeedHTTP(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	empty := okJSON[uiFeedContract](t, u, 200, "GET", "/api/ui/notifications", nil)
	if empty.Notifications == nil || len(empty.Notifications) != 0 || empty.HasMore || empty.NextCursor == "" {
		t.Fatalf("empty: %+v", empty)
	}
	_, n, c := pmMentionsUser(t, s, "decide")
	beforeBell := okJSON[api.Notifications](t, u, 200, "GET", "/api/notifications", nil)
	path := "/api/ui/notifications?cursor=" + empty.NextCursor
	page := okJSON[uiFeedContract](t, u, 200, "GET", path, nil)
	if page.HasMore || len(page.Notifications) != 1 || page.NextCursor == empty.NextCursor {
		t.Fatalf("increment: %+v", page)
	}
	item := page.Notifications[0]
	if item.ID == "" || item.Kind != "mention" || item.Title == "" || item.Body == "" || item.Destination != fmt.Sprintf("/%d?tab=comments&comment=%d", n, c) {
		t.Fatalf("notification: %+v", item)
	}
	replay := okJSON[uiFeedContract](t, userCaller(t, s), 200, "GET", path, nil)
	if !reflect.DeepEqual(page, replay) {
		t.Fatalf("independent retry: %+v != %+v", page, replay)
	}
	caughtUp := okJSON[uiFeedContract](t, u, 200, "GET", "/api/ui/notifications?cursor="+page.NextCursor, nil)
	if len(caughtUp.Notifications) != 0 || caughtUp.Notifications == nil || caughtUp.HasMore || caughtUp.NextCursor != page.NextCursor {
		t.Fatalf("caught up: %+v", caughtUp)
	}
	afterBell := okJSON[api.Notifications](t, u, 200, "GET", "/api/notifications", nil)
	if !reflect.DeepEqual(beforeBell, afterBell) {
		t.Fatal("delivery changed bell marker/list")
	}
	// Bell reads never consume a UI's delivery cursor.
	okJSON[api.Notifications](t, u, 200, "PUT", "/api/notifications/seen", api.NotificationsSeen{Through: c})
	replay = okJSON[uiFeedContract](t, u, 200, "GET", path, nil)
	if !reflect.DeepEqual(page.Notifications, replay.Notifications) {
		t.Fatal("bell seen consumed UI delivery")
	}
	// Scan one event at a time, including empty pages, without dropping notifications.
	cursor := empty.NextCursor
	var items = page.Notifications[:0:0]
	for {
		p := okJSON[uiFeedContract](t, u, 200, "GET", "/api/ui/notifications?limit=1&cursor="+cursor, nil)
		items = append(items, p.Notifications...)
		if p.HasMore && p.NextCursor == cursor {
			t.Fatal("paging did not advance")
		}
		cursor = p.NextCursor
		if !p.HasMore {
			break
		}
	}
	if !reflect.DeepEqual(items, page.Notifications) {
		t.Fatalf("paging: %+v != %+v", items, page.Notifications)
	}
	for _, query := range []string{"cursor=", "cursor=-1", "cursor=x", "cursor=01", "cursor=1&cursor=2", "cursor=9223372036854775808", "limit=bad", "limit=1&limit=2"} {
		u.fails(400, api.CodeBadRequest, "GET", "/api/ui/notifications?"+query, nil)
	}
	u.fails(422, api.CodeValidation, "GET", "/api/ui/notifications?limit=201", nil)
	u.fails(422, api.CodeValidation, "GET", "/api/ui/notifications?limit=-1", nil)
	u.fails(409, api.CodeConflict, "GET", "/api/ui/notifications?cursor=9223372036854775807", nil)
}

func TestUIRoutesAuthentication(t *testing.T) {
	for _, mode := range []string{"token", "basic", "none"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestServer(t, func(c *serverconfig.Config) { c.Auth = mode; c.BasicAuth = "person:password" })
			b := newBrowser(t, s)
			for _, route := range []struct {
				method, path string
				body         any
			}{{"POST", "/api/ui/capabilities", map[string]any{"features": []string{"unknown"}}}, {"GET", "/api/ui/notifications", nil}} {
				hdr := bearer(s)
				if mode == "basic" {
					hdr = map[string]string{"Authorization": "Basic cGVyc29uOnBhc3N3b3Jk"}
				}
				if mode == "none" {
					hdr = map[string]string{}
				}
				resp := b.do(route.method, route.path, route.body, hdr)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("native %s: %d", route.path, resp.StatusCode)
				}
				if resp.Header.Get("Cache-Control") != "no-store" {
					t.Fatal("notification response may be cached")
				}
				if mode != "none" {
					resp = b.do(route.method, route.path, route.body, nil)
					if resp.StatusCode != 401 {
						t.Fatalf("unauthenticated %s: %d", route.path, resp.StatusCode)
					}
				}
				for _, bad := range []map[string]string{{"Host": "evil.example"}, {"Origin": "tauri://localhost"}, {"Sec-Fetch-Site": "cross-site"}} {
					h := map[string]string{}
					for k, v := range hdr {
						h[k] = v
					}
					for k, v := range bad {
						h[k] = v
					}
					resp = b.do(route.method, route.path, route.body, h)
					if resp.StatusCode != 403 {
						t.Fatalf("protection %v: %d", bad, resp.StatusCode)
					}
				}
			}
		})
	}
}

func TestUIRoutesUserOnly(t *testing.T) {
	s := newTestServer(t)
	for _, role := range []api.Role{api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer} {
		session := liveSession(t, s, role)
		a := s.agent(t, session.ID)
		a.fails(403, api.CodeForbidden, "POST", "/api/ui/capabilities", map[string]any{"features": []string{}})
		a.fails(403, api.CodeForbidden, "GET", "/api/ui/notifications", nil)
	}
	// The no-session local socket remains the user, as for existing routes.
	rec := socketDo(t, s, "GET", "/api/ui/notifications", nil, "")
	var feed uiFeedContract
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &feed) != nil {
		t.Fatalf("socket user: %d %s", rec.Code, rec.Body.String())
	}
}
