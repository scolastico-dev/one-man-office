package luabox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Bounds of omo.http.
const (
	DefaultHTTPRequests = 8
	DefaultHTTPTimeout  = 10 * time.Second
	MaxHTTPTimeout      = 30 * time.Second
	MaxHTTPBody         = 1 << 20
	maxRedirects        = 5
	maxResponseHeaders  = 64 << 10
)

// Fixed phrases of omo.http. A URL, a header or a body is never put into
// one.
const (
	phraseHTTPTimeout   = "http request timed out"
	phraseHTTPFailed    = "http request failed"
	phraseHTTPTooLarge  = "http response body exceeds 1048576 bytes"
	phraseHTTPTooMany   = "too many http requests"
	phraseHTTPReach     = "http address not allowed"
	phraseHTTPRedirect  = "http redirect not allowed"
	phraseHTTPBodyLarge = "http request body exceeds 1048576 bytes"
)

var (
	errHTTPRedirect = errors.New(phraseHTTPRedirect)
	errHTTPTooMany  = errors.New("too many redirects")
)

// HTTP is the omo.http helper: omo.http{method?, url, headers?, json? |
// form? | body?, timeout?} returns {status, body, headers} or nil and a
// fixed phrase.
//
// It reaches https on any host and http only on loopback (the name
// localhost or a literal address in 127.0.0.0/8 or ::1; no lookup decides
// it). Redirects are followed only to the same scheme rules and the same
// host and port, at most 5. No cookies are kept. Each request has a
// timeout (a number of seconds or a duration text; default 10 s, at most
// 30 s, never past the run's deadline). A run makes at most MaxRequests
// calls; request and response bodies are at most 1 MiB. Every request
// header value, and the token of a "<scheme> <token>" value, is registered
// with the run's redactor.
type HTTP struct {
	Client      *http.Client // nil: a fresh client per box, proxy from the environment, no keep-alive; tests inject one
	MaxRequests int          // 0 = 8
}

type httpRequest struct {
	method  string
	url     *url.URL
	headers http.Header
	body    []byte
	timeout time.Duration
}

// Func returns the helper bound to b; register it with b.Set("http", …).
func (h *HTTP) Func(b *Box) lua.LGFunction {
	max := h.MaxRequests
	if max <= 0 {
		max = DefaultHTTPRequests
	}
	client := h.client(b)
	used := 0
	return func(L *lua.LState) int {
		req, phrase := parseHTTPRequest(L.Get(1))
		if phrase != "" {
			return pushFail(L, phrase)
		}
		if !reachable(req.url) {
			return pushFail(L, phraseHTTPReach)
		}
		if used >= max {
			return pushFail(L, phraseHTTPTooMany)
		}
		used++
		if req.url.User != nil {
			if pw, ok := req.url.User.Password(); ok {
				b.red.Add(pw)
			}
		}
		for _, values := range req.headers {
			for _, v := range values {
				b.red.Add(v)
				if _, token, ok := strings.Cut(v, " "); ok {
					b.red.Add(strings.TrimSpace(token))
				}
			}
		}
		status, body, headers, phrase := doHTTP(b.ctx, client, req)
		if phrase != "" {
			return pushFail(L, phrase)
		}
		lv, err := FromGo(L, map[string]any{"status": status, "body": body, "headers": headers})
		if err != nil {
			return pushFail(L, phraseHTTPFailed)
		}
		L.Push(lv)
		return 1
	}
}

// client is a copy of the injected client, or a fresh one, with the
// redirect rule and without cookies.
func (h *HTTP) client(b *Box) *http.Client {
	var c http.Client
	if h.Client != nil {
		c = *h.Client
	} else {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DisableKeepAlives = true
		tr.MaxResponseHeaderBytes = maxResponseHeaders
		c.Transport = tr
		b.onClose(tr.CloseIdleConnections)
	}
	c.Jar = nil
	c.Timeout = 0
	c.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errHTTPTooMany
		}
		if !reachable(next.URL) || hostPort(next.URL) != hostPort(via[0].URL) {
			return errHTTPRedirect
		}
		return nil
	}
	return &c
}

// reachable is the reach rule: https anywhere, http only on loopback.
func reachable(u *url.URL) bool {
	if u.Host == "" || u.Hostname() == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		return isLoopback(u.Hostname())
	}
	return false
}

func isLoopback(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func doHTTP(ctx context.Context, client *http.Client, r httpRequest) (int, string, map[string][]string, string) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.method, r.url.String(), bytes.NewReader(r.body))
	if err != nil {
		return 0, "", nil, phraseHTTPFailed
	}
	req.Header = r.headers
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", nil, httpPhrase(ctx, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxHTTPBody+1))
	if err != nil {
		return 0, "", nil, httpPhrase(ctx, err)
	}
	if len(body) > MaxHTTPBody {
		return 0, "", nil, phraseHTTPTooLarge
	}
	headers := make(map[string][]string, len(resp.Header))
	for name, values := range resp.Header {
		headers[name] = append([]string(nil), values...)
	}
	return resp.StatusCode, string(body), headers, ""
}

// httpPhrase maps an error of the client to a fixed phrase.
func httpPhrase(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, errHTTPRedirect), errors.Is(err, errHTTPTooMany):
		return phraseHTTPRedirect
	case ctx.Err() != nil, errors.Is(err, context.DeadlineExceeded):
		return phraseHTTPTimeout
	}
	return phraseHTTPFailed
}

// parseHTTPRequest reads the request table; a refusal is a fixed phrase.
func parseHTTPRequest(v lua.LValue) (httpRequest, string) {
	t, ok := v.(*lua.LTable)
	if !ok {
		return httpRequest{}, "http request must be a table"
	}
	allowed := map[string]bool{"method": true, "url": true, "headers": true, "form": true, "json": true, "body": true, "timeout": true}
	bad := false
	t.ForEach(func(k, _ lua.LValue) {
		if name, ok := k.(lua.LString); !ok || !allowed[string(name)] {
			bad = true
		}
	})
	if bad {
		return httpRequest{}, "http request contains an unknown field"
	}
	r := httpRequest{method: http.MethodGet, headers: http.Header{}, timeout: DefaultHTTPTimeout}
	if m := t.RawGetString("method"); m != lua.LNil {
		s, ok := m.(lua.LString)
		if !ok || !validMethod(string(s)) {
			return httpRequest{}, "http method must be a token"
		}
		r.method = strings.ToUpper(string(s))
	}
	us, ok := t.RawGetString("url").(lua.LString)
	if !ok {
		return httpRequest{}, "http url must be a string"
	}
	u, err := url.Parse(string(us))
	if err != nil {
		return httpRequest{}, phraseHTTPReach
	}
	r.url = u
	if h := t.RawGetString("headers"); h != lua.LNil {
		ht, ok := h.(*lua.LTable)
		if !ok {
			return httpRequest{}, "http headers must be a string-to-string table"
		}
		phrase := ""
		ht.ForEach(func(k, v lua.LValue) {
			name, nok := k.(lua.LString)
			value, vok := v.(lua.LString)
			if !nok || !vok || !validHeaderName(string(name)) || strings.ContainsAny(string(value), "\r\n\x00") {
				phrase = "http headers must be a string-to-string table"
				return
			}
			r.headers.Set(textproto.CanonicalMIMEHeaderKey(string(name)), string(value))
		})
		if phrase != "" {
			return httpRequest{}, phrase
		}
	}
	bodies := 0
	for _, name := range []string{"form", "json", "body"} {
		if t.RawGetString(name) != lua.LNil {
			bodies++
		}
	}
	if bodies > 1 {
		return httpRequest{}, "http form, json and body are mutually exclusive"
	}
	if f := t.RawGetString("form"); f != lua.LNil {
		ft, ok := f.(*lua.LTable)
		if !ok {
			return httpRequest{}, "http form must be a string-to-string table"
		}
		form := url.Values{}
		bad := false
		ft.ForEach(func(k, v lua.LValue) {
			name, nok := k.(lua.LString)
			value, vok := v.(lua.LString)
			if !nok || !vok {
				bad = true
				return
			}
			form.Add(string(name), string(value))
		})
		if bad {
			return httpRequest{}, "http form must be a string-to-string table"
		}
		r.body = []byte(form.Encode())
		if r.headers.Get("Content-Type") == "" {
			r.headers.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if j := t.RawGetString("json"); j != lua.LNil {
		v, err := ToGo(j)
		if err != nil {
			return httpRequest{}, "http json body could not be encoded"
		}
		if err := checkFinite(v, 0); err != nil {
			return httpRequest{}, "http json body could not be encoded"
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return httpRequest{}, "http json body could not be encoded"
		}
		r.body = raw
		if r.headers.Get("Content-Type") == "" {
			r.headers.Set("Content-Type", "application/json")
		}
	}
	if bv := t.RawGetString("body"); bv != lua.LNil {
		s, ok := bv.(lua.LString)
		if !ok {
			return httpRequest{}, "http body must be a string"
		}
		r.body = []byte(s)
	}
	if len(r.body) > MaxHTTPBody {
		return httpRequest{}, phraseHTTPBodyLarge
	}
	if tv := t.RawGetString("timeout"); tv != lua.LNil {
		var d time.Duration
		switch x := tv.(type) {
		case lua.LNumber:
			d = time.Duration(float64(x) * float64(time.Second))
		case lua.LString:
			d, err = time.ParseDuration(string(x))
			if err != nil {
				return httpRequest{}, "http timeout must be seconds or a duration"
			}
		default:
			return httpRequest{}, "http timeout must be seconds or a duration"
		}
		if d <= 0 {
			return httpRequest{}, "http timeout must be positive"
		}
		r.timeout = min(d, MaxHTTPTimeout)
	}
	return r, ""
}

func validMethod(m string) bool {
	if m == "" || len(m) > 16 {
		return false
	}
	for _, c := range m {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func validHeaderName(n string) bool {
	if n == "" || len(n) > 128 {
		return false
	}
	for _, c := range n {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
