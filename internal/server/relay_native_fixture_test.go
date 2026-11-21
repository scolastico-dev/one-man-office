package server

// This opt-in fixture exposes only a line-oriented test control pipe. The real
// server binds no browser port: all board traffic must use its relay callback.
import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/relayhost"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
	"github.com/scolastico-dev/one-man-office/internal/relayserver"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

var nativeRelayFixture = flag.Bool("native-relay-fixture", false, "serve isolated native relay board test control on stdin/stdout")

var nativeRelayNotificationsFixture = flag.Bool("native-relay-notifications-fixture", false, "exercise headless notification polling without board assets")

func TestNativeRelayBoardFixture(t *testing.T) {
	if !*nativeRelayFixture {
		return
	}
	// The in-memory runner never starts git or an agent program.
	oldGitVersion := gitVersion
	gitVersion = func() (int, int, error) { return 2, 47, nil }
	t.Cleanup(func() { gitVersion = oldGitVersion })
	if !*nativeRelayNotificationsFixture && !uiBuilt() {
		t.Fatal("build web assets before running the native board probe")
	}
	layout := testHome(t)
	passwordFile := filepath.Join(t.TempDir(), "relay-password")
	if err := os.WriteFile(passwordFile, []byte("board-fixture-admission"), 0600); err != nil {
		t.Fatal(err)
	}
	var router *relayserver.Server
	var relayHTTP *http.Server
	var relayDone chan struct{}
	address := "127.0.0.1:0"
	notificationHeld := make(chan struct{})
	notificationRelease := make(chan struct{})
	var notificationReply atomic.Value
	notificationReply.Store("")
	requests := make(map[string]bool) // handler lock below protects simultaneous GETs
	var requestLock sync.Mutex
	startRelay := func() {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		address = listener.Addr().String()
		router, err = relayserver.New(relayserver.Config{PasswordFile: passwordFile})
		if err != nil {
			t.Fatal(err)
		}
		relayHTTP = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/notifications/") {
				router.ServeHTTP(w, r)
				return
			}
			header, e := base64.RawURLEncoding.DecodeString(r.Header.Get("Omo-Notification-Request"))
			var poll map[string]any
			if e != nil || json.Unmarshal(header, &poll) != nil {
				t.Error("invalid native notification header")
			}
			request, _ := poll["request"].(string)
			requestLock.Lock()
			if request == "" || requests[request] {
				t.Error("native notification request id reused")
			}
			requests[request] = true
			requestLock.Unlock()
			mode := notificationReply.Swap("").(string)
			if mode == "" {
				router.ServeHTTP(w, r)
				return
			}
			record := httptest.NewRecorder()
			router.ServeHTTP(record, r)
			if record.Code != 200 {
				t.Errorf("injected reply did not reach host: %d", record.Code)
			}
			if mode == "hold" {
				close(notificationHeld)
				select {
				case <-notificationRelease:
				case <-r.Context().Done():
					return
				}
				for key, values := range record.Header() {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				_, _ = w.Write(record.Body.Bytes())
				return
			}
			if mode == "drop" {
				w.WriteHeader(204)
				return
			}
			if mode == "error" {
				w.WriteHeader(503)
				return
			}
			var envelope map[string]any
			if json.Unmarshal(record.Body.Bytes(), &envelope) != nil {
				t.Error("invalid host envelope")
				return
			}
			field := map[string]string{"tamper": "ciphertext", "wrong-request": "request", "wrong-host": "host_fp", "wrong-device": "ui_id", "wrong-epoch": "pair_epoch"}[mode]
			value, _ := envelope[field].(string)
			if len(value) > 0 {
				first := byte('A')
				if value[0] == first {
					first = 'B'
				}
				envelope[field] = string(first) + value[1:]
			}
			for key, values := range record.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			_ = json.NewEncoder(w).Encode(envelope)
		})}
		relayDone = make(chan struct{})
		go func() { defer close(relayDone); _ = relayHTTP.Serve(listener) }()
	}
	stopRelay := func() {
		if router != nil {
			_ = router.Close()
			_ = relayHTTP.Close()
			<-relayDone
			router = nil
		}
	}
	startRelay()
	defer stopRelay()
	origin := "http://" + address
	var s *Server
	var agent *testAgent
	var ceo board.Actor
	firstHost := true
	startHost := func() {
		c, err := serverconfig.Load(layout.ServerConfig)
		if err != nil {
			t.Fatal(err)
		}
		runner := &testRunner{}
		s, err = newServer(Options{Layout: layout, Config: c, Version: "native-board-test", Runner: runner}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if firstHost {
			if err = s.settings.PutProfileIf("", fakeProfile()); err != nil {
				t.Fatal(err)
			}
			if err = s.settings.PutRoleIf("", settings.Role{Name: "ceo", Models: []string{"fake/a"}, Assignment: api.AssignmentRoundRobin}); err != nil {
				t.Fatal(err)
			}
			firstHost = false
		}
		s.StartEngine()
		<-s.engineReady
		session, err := s.engine.StartCEO(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		agent = runner.agent(t, session.ID)
		ceo = board.Actor{Role: api.RoleCEO, Session: session.ID}
	}
	stopHost := func() {
		if s != nil {
			_ = s.Close()
			s = nil
		}
	}
	defer stopHost()
	// Uses the same durable global-home layout after every restart.
	c := serverconfig.Defaults()
	if err := serverconfig.Save(layout.ServerConfig, c); err != nil {
		t.Fatal(err)
	}
	startHost()
	password := "board-fixture-admission"
	if err := s.relay.Connect(relayhost.ConnectOptions{Origin: origin, ClientID: "native-board", Password: &password}); err != nil {
		t.Fatal(err)
	}
	waitRelayState(t, func() bool { return s.relay.Status().State == "connected" })
	ticket, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "native upload"})
	if err != nil {
		t.Fatal(err)
	}
	n := ticket.Number
	info := func() any {
		return map[string]any{"origin": origin, "host_id": "native-board", "admission": password, "ticket": n, "terminal": agent.termID}
	}
	encoder := json.NewEncoder(os.Stdout)
	reply := func(data any, err error) {
		result := map[string]any{"ok": err == nil, "data": data}
		if err != nil {
			result["error"] = err.Error()
		}
		if err := encoder.Encode(result); err != nil {
			t.Fatal(err)
		}
	}
	reply(info(), nil)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var command struct {
			Op    string `json:"op"`
			AID   string `json:"aid"`
			PIN   string `json:"pin"`
			UIID  string `json:"ui_id"`
			Count int    `json:"count"`
			Mode  string `json:"mode"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &command); err != nil {
			t.Fatal(err)
		}
		var data any
		var err error
		switch command.Op {
		case "mention":
			ids := []string{}
			for range command.Count {
				comment, e := s.board.Comment(t.Context(), ceo, int64(n), api.CommentRequest{Body: "@user native notification"})
				if e != nil {
					err = e
					break
				}
				ids = append(ids, strconv.Itoa(comment.Comment.ID))
			}
			data = ids
		case "wait-notification-held":
			<-notificationHeld
		case "release-notification":
			close(notificationRelease)
		case "notification-reply":
			notificationReply.Store(command.Mode)
		case "connections":
			data = s.relay.LiveConnections(command.UIID)
		case "begin":
			err = s.relay.PairBegin()
		case "pending":
			waitRelayState(t, func() bool {
				pending, e := s.relay.PairPending()
				return e == nil && pending.Phase == relayproto.PairProved
			})
			data, err = s.relay.PairPending()
		case "accept":
			aid, e := base64.RawURLEncoding.DecodeString(command.AID)
			if e != nil {
				t.Fatal(e)
			}
			var device relayhost.Device
			device, err = s.relay.PairAccept(aid, command.PIN, nil, "native-board")
			if err == nil {
				data = map[string]any{"ui_id": device.UIID}
			}
		case "stop-host":
			stopHost()
		case "start-host":
			startHost()
			waitRelayState(t, func() bool { return s.relay.Status().State == "connected" })
			data = info()
		case "stop-relay":
			stopRelay()
		case "start-relay":
			startRelay()
			waitRelayState(t, func() bool { return s.relay.Status().State == "connected" })
		case "typed":
			data = agent.proc.typed()
		case "wait-write", "write-count":
			count := func() int {
				comments, e := s.board.Comments(t.Context(), board.User, int64(n))
				if e != nil {
					t.Fatal(e)
				}
				found := 0
				for _, comment := range comments {
					if comment.Body == "uncertain-once" {
						found++
					}
				}
				return found
			}
			if command.Op == "wait-write" {
				waitRelayState(t, func() bool { return count() > 0 })
			}
			data = count()
		case "output":
			agent.say("native relay output")
		case "revoke":
			err = s.relay.Unregister(command.UIID)
		default:
			t.Fatalf("unknown fixture command %q", command.Op)
		}
		reply(data, err)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
