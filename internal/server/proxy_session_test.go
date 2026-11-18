package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/proxy"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

func proxyResultURL(t *testing.T, res *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	var result proxyOpenResult
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestProxyOpenReusesHostSession(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(strconv.FormatBool(full), func(t *testing.T) {
			s := newTestServer(t, subdomainConfig, func(c *serverconfig.Config) { c.PublicURL = "https://board.example" })
			f := proxyForward(t, s, "http://127.0.0.1:1", false)
			f2 := proxyForward(t, s, "http://127.0.0.1:1", false)
			h := http.Header{"Authorization": {"Bearer " + s.token}, "Content-Type": {"application/json"}}
			open := subdomainRequest(t, s, "POST", "https://board.example/api/proxy/forwards/"+f.ID+"/open", "{}", h)
			wantStatus(t, open.Code, 200)
			var session string
			for _, c := range open.Result().Cookies() {
				if c.Name == proxy.SessionCookie {
					session = c.Value
					if c.Domain != "" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 43200 {
						t.Fatalf("host-local cookie: %+v", c)
					}
				}
			}
			if session == "" {
				t.Fatal("missing bootstrap session")
			}
			// Give the session an older, fixed expiry to detect sliding TTL.
			expiry := time.Now().Add(time.Hour)
			s.proxyAuth.sessions[session] = expiry
			if full {
				for i := range proxyAuthLimit - 1 {
					s.proxyAuth.sessions[strconv.Itoa(i)] = expiry
				}
			}
			before := len(s.proxyAuth.sessions)
			h.Set("Cookie", proxy.SessionCookie+"="+session+"; "+proxy.ForwardCookie+"="+f.ID)
			for range 5 {
				res := subdomainRequest(t, s, "POST", "https://board.example/api/proxy/forwards/"+f2.ID+"/open", "{}", h)
				wantStatus(t, res.Code, 200)
				if len(s.proxyAuth.sessions) != before || !s.proxyAuth.sessions[session].Equal(expiry) {
					t.Fatal("Open replaced the session or extended its expiry")
				}
				cookies := res.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != proxy.ForwardCookie || cookies[0].Value != f2.ID {
					t.Fatalf("Open must only update forward selection: %+v", cookies)
				}
				if proxyResultURL(t, res).Fragment == "" {
					t.Fatal("Open omitted the destination grant")
				}
			}
			// A valid proxy cookie does not substitute for board authentication.
			h.Del("Authorization")
			res := subdomainRequest(t, s, "POST", "https://board.example/api/proxy/forwards/"+f2.ID+"/open", "{}", h)
			wantStatus(t, res.Code, 401)
			if len(res.Result().Cookies()) != 0 || len(s.proxyAuth.sessions) != before {
				t.Fatal("unauthenticated Open changed session state")
			}
		})
	}
}

func TestProxyExchangeReusesSessionAcrossOpens(t *testing.T) {
	for _, mode := range []string{"path", "same-base", "outside-base"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestServer(t, func(c *serverconfig.Config) {
				c.Port = 8090
				if mode != "path" {
					subdomainConfig(c)
				}
			})
			boardOrigin := "http://omo.localhost:8090"
			if mode == "same-base" {
				boardOrigin = "http://apps.localhost:8090"
			}
			f := proxyForward(t, s, "http://127.0.0.1:1", false)
			f2 := proxyForward(t, s, "http://127.0.0.1:1", false)
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			request := func(raw string, grant string) *httptest.ResponseRecorder {
				u, err := url.Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
				h := http.Header{"Content-Type": {"application/json"}, "Origin": {u.Scheme + "://" + u.Host}}
				body := "{}"
				if grant == "" {
					h.Set("Authorization", "Bearer "+s.token)
				} else {
					body = `{"grant":"` + grant + `"}`
				}
				for _, c := range jar.Cookies(u) {
					h.Add("Cookie", c.Name+"="+c.Value)
				}
				res := subdomainRequest(t, s, "POST", raw, body, h)
				jar.SetCookies(u, res.Result().Cookies())
				return res
			}
			openAndExchange := func(id string) *httptest.ResponseRecorder {
				open := request(boardOrigin+"/api/proxy/forwards/"+id+"/open", "")
				wantStatus(t, open.Code, 200)
				u := proxyResultURL(t, open)
				ex := request(u.Scheme+"://"+u.Host+"/_omo/proxy/exchange", u.Fragment)
				wantStatus(t, ex.Code, 200)
				return ex
			}
			first := openAndExchange(f.ID)
			wantSessions := 1
			if mode == "outside-base" {
				wantSessions = 2 // Separate cookie domains require separate sessions.
			}
			if len(s.proxyAuth.sessions) != wantSessions {
				t.Fatalf("bootstrap sessions: %d", len(s.proxyAuth.sessions))
			}
			expiry := time.Now().Add(time.Hour)
			for key := range s.proxyAuth.sessions {
				s.proxyAuth.sessions[key] = expiry
			}
			var session string
			for _, c := range first.Result().Cookies() {
				if c.Name == proxy.SessionCookie {
					session = c.Value
				}
			}
			if session == "" {
				t.Fatal("missing exchanged session")
			}
			for i := range 5 {
				ex := openAndExchange(f2.ID)
				if len(s.proxyAuth.sessions) != wantSessions || !s.proxyAuth.sessions[session].Equal(expiry) {
					t.Fatal("exchange replaced a valid session or extended expiry")
				}
				cookies := ex.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != proxy.ForwardCookie || cookies[0].Value != f2.ID {
					t.Fatalf("exchange must only update forward selection: %+v", cookies)
				}
				if proxyResultURL(t, ex).String() != boardOrigin+f2.Address && mode == "path" {
					t.Fatal("wrong path destination")
				}
				if i == 0 || i == 1 {
					// Repeat near capacity, then at capacity, with the same cookies.
					for n := len(s.proxyAuth.sessions); n < proxyAuthLimit-1+i; n++ {
						s.proxyAuth.sessions[strconv.Itoa(n)] = expiry
					}
					wantSessions = proxyAuthLimit - 1 + i
				}
			}
			// Reusing a session must still require a live, unused grant.
			u := proxyResultURL(t, first)
			invalid := request(u.Scheme+"://"+u.Host+"/_omo/proxy/exchange", "invalid")
			wantStatus(t, invalid.Code, 401)
			if len(invalid.Result().Cookies()) != 0 || len(s.proxyAuth.sessions) != wantSessions {
				t.Fatal("invalid grant changed session state")
			}
		})
	}
}

func TestProxySessionInvalidCookieRecovery(t *testing.T) {
	for _, endpoint := range []string{"open", "exchange"} {
		for _, cookie := range []string{"missing", "tampered", "expired", "duplicate"} {
			t.Run(endpoint+"/"+cookie, func(t *testing.T) {
				s := newTestServer(t, subdomainConfig)
				f := proxyForward(t, s, "http://127.0.0.1:1", false)
				expiry := time.Now().Add(time.Hour)
				if cookie == "expired" {
					expiry = time.Now().Add(-time.Hour)
				}
				s.proxyAuth.sessions = map[string]time.Time{"existing": expiry}
				h := http.Header{"Content-Type": {"application/json"}}
				switch cookie {
				case "tampered":
					h.Set("Cookie", proxy.SessionCookie+"=existing-tampered")
				case "expired":
					h.Set("Cookie", proxy.SessionCookie+"=existing")
				case "duplicate":
					h.Add("Cookie", proxy.SessionCookie+"=existing")
					h.Add("Cookie", proxy.SessionCookie+"=existing")
				}
				origin := "http://omo.localhost:8090"
				path, body := "/api/proxy/forwards/"+f.ID+"/open", "{}"
				if endpoint == "open" {
					h.Set("Authorization", "Bearer "+s.token)
				} else {
					origin = "http://dev-" + f.ID + "-user.apps.localhost:8090"
					grant, err := s.proxyAuth.issue(proxyDestination{ForwardID: f.ID, Origin: origin, URL: origin + f.StartPath}, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					path, body = "/_omo/proxy/exchange", `{"grant":"`+grant+`"}`
				}
				res := subdomainRequest(t, s, "POST", origin+path, body, h)
				wantStatus(t, res.Code, 200)
				var replacement string
				for _, c := range res.Result().Cookies() {
					if c.Name == proxy.SessionCookie {
						replacement = c.Value
					}
				}
				if replacement == "" || replacement == "existing" || !s.proxyAuth.authorized(replacement, time.Now()) {
					t.Fatal("invalid cookie was trusted instead of creating a valid session")
				}
				if cookie == "expired" {
					if _, exists := s.proxyAuth.sessions["existing"]; exists {
						t.Fatal("expired session retained")
					}
				} else if !s.proxyAuth.sessions["existing"].Equal(expiry) {
					t.Fatal("unrelated valid session changed")
				}
			})
		}
	}
}

func TestProxyFailedExchangePreservesExistingSession(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) { c.Port = 8090 })
	f := proxyForward(t, s, "http://127.0.0.1:1", false)
	expiry := time.Now().Add(time.Hour)
	s.proxyAuth.sessions = map[string]time.Time{"existing": expiry}
	origin := "http://omo.localhost:8090"
	grant, err := s.proxyAuth.issue(proxyDestination{ForwardID: f.ID, Origin: origin, URL: origin + f.BasePath}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.board.DeleteProxyForward(context.Background(), board.User, f.ID); err != nil {
		t.Fatal(err)
	}
	h := http.Header{"Content-Type": {"application/json"}, "Cookie": {proxy.SessionCookie + "=existing"}}
	res := subdomainRequest(t, s, "POST", origin+"/_omo/proxy/exchange", `{"grant":"`+grant+`"}`, h)
	wantStatus(t, res.Code, 401)
	if len(res.Result().Cookies()) != 0 || len(s.proxyAuth.sessions) != 1 || !s.proxyAuth.sessions["existing"].Equal(expiry) || len(s.proxyAuth.grants) != 0 {
		t.Fatal("failed exchange changed existing credentials or retained a grant/session")
	}
}

func TestProxyOpenGrantCapacityPreservesSessions(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			s := newTestServer(t, subdomainConfig)
			f := proxyForward(t, s, "http://127.0.0.1:1", false)
			expiry := time.Now().Add(time.Hour)
			s.proxyAuth.grants = make(map[string]proxyGrant)
			for i := range proxyAuthLimit {
				s.proxyAuth.grants[strconv.Itoa(i)] = proxyGrant{expires: expiry}
			}
			h := http.Header{"Authorization": {"Bearer " + s.token}, "Content-Type": {"application/json"}}
			if existing {
				s.proxyAuth.sessions = map[string]time.Time{"existing": expiry}
				h.Set("Cookie", proxy.SessionCookie+"=existing")
			}
			before := len(s.proxyAuth.sessions)
			res := subdomainRequest(t, s, "POST", "http://omo.localhost:8090/api/proxy/forwards/"+f.ID+"/open", "{}", h)
			wantStatus(t, res.Code, 503)
			if len(res.Result().Cookies()) != 0 || len(s.proxyAuth.grants) != proxyAuthLimit || len(s.proxyAuth.sessions) != before {
				t.Fatal("failed Open issued credentials or leaked state")
			}
			if existing && !s.proxyAuth.sessions["existing"].Equal(expiry) {
				t.Fatal("failed Open changed expiry")
			}
		})
	}
}
