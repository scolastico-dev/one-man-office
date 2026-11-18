package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stream progress")
		var zero T
		return zero
	}
}

func liveProxy(t *testing.T, upstream *httptest.Server, mode Mode, strip bool, values ...RequestContext) *httptest.Server {
	t.Helper()
	tr := NewTransport()
	t.Cleanup(tr.CloseIdleConnections)
	f := forwardFor(t, upstream.URL)
	f.StripPrefix = strip
	handler := tr.Handler(f, mode)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(values) > 0 {
			r = WithRequestContext(r, values[0])
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

func streamRequest(t *testing.T, method, address string, body io.Reader) *http.Request {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestEarlyStreaming(t *testing.T) {
	for _, kind := range []string{"sse", "chunked", "known length"} {
		t.Run(kind, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "sse":
					w.Header().Set("Content-Type", "text/event-stream")
				case "known length":
					w.Header().Set("Content-Length", "19")
				}
				fmt.Fprint(w, "data: first\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
					fmt.Fprint(w, "tail\n\n")
				case <-r.Context().Done():
				}
			}))
			defer upstream.Close()
			proxy := liveProxy(t, upstream, PathMode, false)
			resp, err := http.DefaultClient.Do(streamRequest(t, "GET", proxy.URL+"/proxy/abc/events", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			first := make([]byte, len("data: first\n\n"))
			if _, err = io.ReadFull(resp.Body, first); err != nil {
				t.Fatal(err)
			}
			if string(first) != "data: first\n\n" {
				t.Fatalf("early bytes: %q", first)
			}
			// The upstream is still blocked on release. A fully buffering proxy deadlocks
			// here until the request deadline and cannot pass the first ReadFull.
		})
	}
}

func TestLargeStreamedUploadAndDownload(t *testing.T) {
	const size = 8 << 20
	payload := bytes.Repeat([]byte("abcdefgh"), size/8)
	uploadProgress := make(chan struct{}, 1)
	allowDownload := make(chan struct{})
	defer close(allowDownload)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first := make([]byte, 8)
		if _, err := io.ReadFull(r.Body, first); err != nil {
			return
		}
		uploadProgress <- struct{}{}
		hash := sha256.New()
		hash.Write(first)
		n, err := io.Copy(hash, r.Body)
		if err != nil || n+8 != size || !bytes.Equal(hash.Sum(nil), sha256Sum(payload)) {
			http.Error(w, "upload mismatch", 400)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(size))
		w.Write(payload[:8])
		w.(http.Flusher).Flush()
		select {
		case <-allowDownload:
			w.Write(payload[8:])
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	proxy := liveProxy(t, upstream, PathMode, true)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	result := make(chan *http.Response, 1)
	failure := make(chan error, 1)
	req := streamRequest(t, "POST", proxy.URL+"/proxy/abc/upload", reader)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			failure <- err
			return
		}
		result <- resp
	}()
	if _, err := writer.Write(payload[:8]); err != nil {
		t.Fatal(err)
	}
	await(t, uploadProgress) // Cannot happen if the proxy buffers the whole upload.
	if _, err := writer.Write(payload[8:]); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	var resp *http.Response
	select {
	case resp = <-result:
	case err := <-failure:
		t.Fatal(err)
	case <-req.Context().Done():
		t.Fatal(req.Context().Err())
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("upload response: %d", resp.StatusCode)
	}
	first := make([]byte, 8)
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, payload[:8]) {
		t.Fatal("wrong early download")
	}
	allowDownload <- struct{}{} // Release only after the client sees the first bytes.
	hash := sha256.New()
	hash.Write(first)
	n, err := io.Copy(hash, resp.Body)
	if err != nil || n+8 != size || !bytes.Equal(hash.Sum(nil), sha256Sum(payload)) {
		t.Fatalf("download mismatch: %d %v", n, err)
	}
}
func sha256Sum(data []byte) []byte { sum := sha256.Sum256(data); return sum[:] }

func TestCancellationReachesUpstream(t *testing.T) {
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "started\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	proxy := liveProxy(t, upstream, SubdomainMode, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := streamRequest(t, "GET", proxy.URL+"/events", nil).WithContext(ctx)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, 8)
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	cancel()
	await(t, cancelled)
}

func TestWebSocketEchoAndCredentialIsolation(t *testing.T) {
	for _, tc := range []struct {
		mode   Mode
		remove bool
	}{{PathMode, false}, {PathMode, true}, {SubdomainMode, false}, {SubdomainMode, true}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			mode := tc.mode
			captured := make(chan *http.Request, 1)
			serverErrors := make(chan error, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured <- r.Clone(context.Background())
				c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, Subprotocols: []string{"chat"}})
				if err != nil {
					serverErrors <- err
					return
				}
				defer c.CloseNow()
				c.SetReadLimit(1 << 20)
				for i := 0; i < 2; i++ {
					typ, data, err := c.Read(r.Context())
					if err != nil {
						serverErrors <- err
						return
					}
					if err = c.Write(r.Context(), typ, data); err != nil {
						serverErrors <- err
						return
					}
				}
			}))
			defer upstream.Close()
			proxy := liveProxy(t, upstream, mode, true, RequestContext{RemoveAuthorization: tc.remove})
			path := "/proxy/abc/socket"
			if mode == SubdomainMode {
				path = "/socket"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			auth := "Bearer app-key"
			if tc.remove {
				auth = "Basic b21vOmNyZWRlbnRpYWw="
			}
			c, resp, err := websocket.Dial(ctx, proxy.URL+path+"?__omo_proxy_grant=SECRET", &websocket.DialOptions{Subprotocols: []string{"omo-token.SECRET", "chat"}, HTTPHeader: http.Header{"Cookie": {"app=ok; omo_proxy_session=SECRET", "omo_proxy_forward=SECRET; other=yes"}, "X-Omo-Session": {"SECRET"}, "Authorization": {auth}}})
			if resp != nil && resp.Body != nil {
				defer resp.Body.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer c.CloseNow()
			c.SetReadLimit(1 << 20)
			if c.Subprotocol() != "chat" {
				t.Fatalf("subprotocol: %q", c.Subprotocol())
			}
			seen := await(t, captured)
			h := seen.Header
			if seen.RequestURI != "/socket" {
				t.Fatalf("WebSocket URI: %q", seen.RequestURI)
			}
			wantAuth := auth
			if tc.remove {
				wantAuth = ""
			}
			if strings.Contains(fmt.Sprint(h), "SECRET") || h.Get("Authorization") != wantAuth || strings.Join(h.Values("Cookie"), ";") != "app=ok;other=yes" {
				t.Fatalf("credentials: %v", h)
			}
			for _, data := range []string{"browser to upstream", strings.Repeat("echo", 16384)} {
				if err = c.Write(ctx, websocket.MessageText, []byte(data)); err != nil {
					t.Fatal(err)
				}
				typ, got, err := c.Read(ctx)
				if err != nil || typ != websocket.MessageText || string(got) != data {
					t.Fatalf("echo type=%v length=%d err=%v", typ, len(got), err)
				}
			}
			select {
			case err := <-serverErrors:
				t.Fatal(err)
			default:
			}
		})
	}
}

func TestCancelledRequestBeforeHeadersHasNoUnavailablePage(t *testing.T) {
	started := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer upstream.Close()
	tr := NewTransport()
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "/proxy/abc/", nil).WithContext(ctx)
	reported := make(chan error, 1)
	req = WithRequestContext(req, RequestContext{OnError: func(err error) { reported <- err }})
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { tr.Handler(forwardFor(t, upstream.URL), PathMode).ServeHTTP(w, req); close(done) }()
	await(t, started)
	cancel()
	await(t, done)
	await(t, upstreamCancelled)
	if w.Body.Len() != 0 || w.Code == 502 || len(w.Header()) != 0 {
		t.Fatalf("cancelled request wrote unavailable response: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	select {
	case err := <-reported:
		t.Fatalf("client cancellation reported as target failure: %v", err)
	default:
	}
}
