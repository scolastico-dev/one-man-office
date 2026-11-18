package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

// Exercise the shared CLI/MCP commands against the real registry and browser
// router, with no CLI process, session environment or running agent engine.
func TestProxyAgentWorkflow(t *testing.T) {
	for _, mode := range []string{"keep", "strip", "subdomain"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			base := "/"
			publicHost := ""
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.Header.Get("Cookie"), "omo_proxy_") || r.Header.Get("Authorization") != "" || r.Header.Get("X-Omo-Session") != "" {
					t.Error("omo credentials reached upstream")
				}
				switch r.URL.Path {
				case base + "demo":
					io.WriteString(w, `<script src="`+base+`asset.js"></script>`)
				case base + "asset.js":
					w.Header().Set("Cache-Control", "public, max-age=60")
					io.WriteString(w, "asset loaded")
				case base + "events":
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: live\n\n")
					http.NewResponseController(w).Flush()
					<-r.Context().Done()
				case base + "hmr":
					conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{publicHost}})
					if err != nil {
						return
					}
					defer conn.CloseNow()
					kind, data, err := conn.Read(r.Context())
					if err == nil {
						conn.Write(r.Context(), kind, data)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(upstream.Close)
			s := newTestServer(t, func(c *serverconfig.Config) {
				if mode == "subdomain" {
					c.ProxyBaseHost = "omo.localhost"
					c.ProxyLabelPrefix = "dev-"
					c.ProxyLabelSuffix = "-user123"
				}
			})
			b := newBrowser(t, s)
			dev := liveSession(t, s, api.RoleDeveloper)
			client := apiclient.NewWithTransport(apiclient.HandlerTransport(s.SocketHandler()), dev.ID)
			run := func(tool, raw string) aicmd.Result {
				t.Helper()
				for _, command := range aicmd.Commands() {
					if command.Tool != tool {
						continue
					}
					in := reflect.New(command.Input).Interface()
					if err := json.Unmarshal([]byte(raw), in); err != nil {
						t.Fatal(err)
					}
					result, err := command.Run(ctx, client, in)
					if err != nil {
						t.Fatal(err)
					}
					return result
				}
				t.Fatalf("missing shared command %s", tool)
				return aicmd.Result{}
			}
			port := upstream.Listener.Addr().(*net.TCPAddr).Port
			result := run("proxy_add", `{"port":`+strconv.Itoa(port)+`,"name":"preview","start_path":"/demo?q=1","strip_prefix":`+strconv.FormatBool(mode == "strip")+`}`)
			var f api.ProxyForward
			if err := json.Unmarshal(result.Value, &f); err != nil {
				t.Fatal(err)
			}
			if f.Ticket == nil || *f.Ticket != *dev.Ticket || f.Host != "127.0.0.1" || f.Scheme != "http" || f.StripPrefix != (mode == "strip") || f.VerifyTLS {
				t.Fatalf("own-ticket registration/defaults: %+v", f)
			}
			// Path-aware servers must be configured before serving their first request.
			if mode == "keep" {
				base = f.BasePath
			}
			pathGuidance := "required base_path above (prefix kept)"
			if mode == "strip" {
				pathGuidance = "strip_prefix is enabled; serve unprefixed paths"
			}
			if !strings.Contains(result.Text, pathGuidance) || !strings.Contains(result.Text, "Subdomain mode: serve at /") {
				t.Fatalf("registration must explain which base to configure: %s", result.Text)
			}
			listed := run("proxy_list", `{}`)
			var list api.ProxyForwardList
			if err := json.Unmarshal(listed.Value, &list); err != nil || len(list.Forwards) != 1 || list.Forwards[0].ID != f.ID {
				t.Fatalf("list: %s %v", listed.Value, err)
			}

			// The browser transport always dials the temporary board listener, including
			// composed hosts; the requested Host still goes through the real host guard.
			publicHost = strings.TrimPrefix(b.srv.URL, "http://")
			prefix := f.BasePath
			if mode == "subdomain" {
				publicHost = "dev-" + f.ID + "-user123.omo.localhost:" + b.port
				prefix = "/"
			}
			upstream.Start()
			opened := proxyOpen(t, s, b, f)
			if mode == "subdomain" && opened.Host != publicHost {
				t.Fatal(opened)
			}
			exchange, _ := http.NewRequestWithContext(ctx, "POST", b.srv.URL+"/_omo/proxy/exchange", strings.NewReader(`{"grant":"`+opened.Fragment+`"}`))
			exchange.Host = publicHost
			exchange.Header.Set("Content-Type", "application/json")
			exchange.Header.Set("Origin", "http://"+publicHost)
			exchanged, err := b.srv.Client().Do(exchange)
			if err != nil {
				t.Fatal(err)
			}
			defer exchanged.Body.Close()
			wantStatus(t, exchanged.StatusCode, 200)
			var destination proxyOpenResult
			if err := json.NewDecoder(exchanged.Body).Decode(&destination); err != nil {
				t.Fatal(err)
			}
			if destination.URL != "http://"+publicHost+prefix+"demo?q=1" {
				t.Fatal(destination)
			}
			headers := http.Header{}
			for _, cookie := range exchanged.Cookies() {
				headers.Add("Cookie", cookie.Name+"="+cookie.Value)
			}
			request := func(path string) *http.Response {
				t.Helper()
				req, _ := http.NewRequestWithContext(ctx, "GET", b.srv.URL+path, nil)
				req.Host = publicHost
				req.Header = headers.Clone()
				res, err := b.srv.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { res.Body.Close() })
				return res
			}
			for path, want := range map[string]string{prefix + "demo?q=1": `<script src="` + base + `asset.js"></script>`, prefix + "asset.js": "asset loaded"} {
				res := request(path)
				data, err := io.ReadAll(res.Body)
				res.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if res.StatusCode != 200 || string(data) != want || res.Header.Get("Content-Security-Policy") != "" {
					t.Fatalf("%s: %d %s", path, res.StatusCode, data)
				}
			}
			stream := request(prefix + "events")
			line, err := bufio.NewReader(stream.Body).ReadString('\n')
			stream.Body.Close()
			if err != nil || line != "data: live\n" {
				t.Fatalf("SSE before close: %q %v", line, err)
			}
			wsHeaders := headers.Clone()
			wsHeaders.Set("Origin", "http://"+publicHost)
			transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, b.srv.Listener.Addr().String())
			}}
			defer transport.CloseIdleConnections()
			conn, res, err := websocket.Dial(ctx, "ws://"+publicHost+prefix+"hmr", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: wsHeaders})
			if err != nil {
				t.Fatalf("WS: %v %v", err, res)
			}
			defer conn.CloseNow()
			if err := conn.Write(ctx, websocket.MessageText, []byte("reload")); err != nil {
				t.Fatal(err)
			}
			_, data, err := conn.Read(ctx)
			if err != nil || string(data) != "reload" {
				t.Fatalf("WS echo: %q %v", data, err)
			}
			conn.CloseNow()
			run("proxy_remove", `{"id":"`+f.ID+`"}`)
			wantStatus(t, request(prefix+"asset.js").StatusCode, 404)
			if got := run("proxy_list", `{}`); strings.TrimSpace(got.Text) != "no forwards" {
				t.Fatal(got.Text)
			}
		})
	}
}
