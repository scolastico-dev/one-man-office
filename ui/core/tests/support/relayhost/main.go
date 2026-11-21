// relayhost is an opt-in native-client fixture, not a replacement protocol peer.
// It runs the production relay router and durable host manager on loopback.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayhost"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
	"github.com/scolastico-dev/one-man-office/internal/relayserver"
)

const admission = "native-fixture-admission"

type fixture struct {
	dir, address, origin           string
	store                          *relayhost.Store
	manager                        *relayhost.Manager
	router                         *relayserver.Server
	http, bridge                   *http.Server
	serveDone, bridgeDone, runDone chan struct{}
	bridgeCancel                   context.CancelFunc
	bridgeWG                       sync.WaitGroup
	mu                             sync.Mutex
	drop                           chan struct{}
}

func waitFor(ctx context.Context, condition func() (bool, error)) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := condition()
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (f *fixture) start(ctx context.Context, first bool) error {
	address := f.address
	if address == "" {
		address = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	f.address, f.origin = listener.Addr().String(), "http://"+listener.Addr().String()
	f.router, err = relayserver.New(relayserver.Config{PasswordFile: filepath.Join(f.dir, "password")})
	if err != nil {
		listener.Close()
		return err
	}
	f.http = &http.Server{Handler: f.router}
	f.serveDone = make(chan struct{})
	go func() { defer close(f.serveDone); _ = f.http.Serve(listener) }()

	// The bridge changes no protocol messages. Its one fault injection drops the
	// first encrypted host record after acceptance, before the router can see it.
	// This proves recovery when durable acceptance succeeds but its reply is lost.
	bridgeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	bridgeCtx, cancel := context.WithCancel(context.Background())
	f.bridgeCancel = cancel
	f.bridge = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.bridgeWG.Add(1)
		defer f.bridgeWG.Done()
		f.proxy(bridgeCtx, w, r)
	})}
	f.bridgeDone = make(chan struct{})
	go func() { defer close(f.bridgeDone); _ = f.bridge.Serve(bridgeListener) }()
	bridgeURL := "ws://" + bridgeListener.Addr().String() + "/v1/ws"
	f.store, err = relayhost.OpenStore(filepath.Join(f.dir, "host"))
	if err != nil {
		return err
	}
	f.manager, err = relayhost.NewManager(f.store, relayhost.Options{
		Origin: f.origin, Enabled: !first,
		Dial: func(ctx context.Context, _ string, header http.Header) (*websocket.Conn, *http.Response, error) {
			return websocket.Dial(ctx, bridgeURL, &websocket.DialOptions{HTTPHeader: header})
		},
		Callbacks: relayhost.Callbacks{ServeSession: func(ctx context.Context, _ relayhost.Capability, records relayproto.RecordIO) error {
			for {
				record, err := records.Receive(ctx)
				if err != nil {
					return err
				}
				if err = records.Send(ctx, record); err != nil {
					return err
				}
			}
		}},
	})
	if err != nil {
		return err
	}
	if first {
		password := admission
		if err = f.manager.Connect(relayhost.ConnectOptions{Origin: f.origin, ClientID: "native-fixture", Password: &password}); err != nil {
			return err
		}
	}
	f.runDone = make(chan struct{})
	go func() { defer close(f.runDone); f.manager.Run(context.Background()) }()
	return waitFor(ctx, func() (bool, error) { return f.manager.Status().State == "connected", nil })
}

func (f *fixture) proxy(parent context.Context, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	host, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer host.CloseNow()
	headers := http.Header{}
	headers.Set("Authorization", r.Header.Get("Authorization"))
	router, _, err := websocket.Dial(ctx, "ws://"+f.address+"/v1/ws", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		return
	}
	defer router.CloseNow()
	stop := context.AfterFunc(ctx, func() { host.CloseNow(); router.CloseNow() })
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		for {
			kind, raw, err := router.Read(ctx)
			if err != nil {
				return
			}
			if err = host.Write(ctx, kind, raw); err != nil {
				return
			}
		}
	}()
	for {
		kind, raw, err := host.Read(ctx)
		if err != nil {
			break
		}
		control, err := relayproto.DecodeControl(raw)
		if err != nil {
			break
		}
		if control.Type == "forward" {
			inner, err := relayproto.DecodeInner(control.Payload)
			if err != nil {
				break
			}
			if inner.Type == "record" {
				f.mu.Lock()
				drop := f.drop
				f.drop = nil
				f.mu.Unlock()
				if drop != nil {
					close(drop)
					break
				}
			}
		}
		if err = router.Write(ctx, kind, raw); err != nil {
			break
		}
	}
	cancel()
	<-done
}

func (f *fixture) stop() {
	if f.manager != nil {
		_ = f.manager.Close()
		f.manager = nil
	}
	if f.runDone != nil {
		<-f.runDone
		f.runDone = nil
	}
	if f.bridgeCancel != nil {
		f.bridgeCancel()
	}
	if f.bridge != nil {
		_ = f.bridge.Shutdown(context.Background())
		<-f.bridgeDone
		f.bridge = nil
	}
	f.bridgeWG.Wait()
	if f.router != nil {
		_ = f.router.Close()
		f.router = nil
	}
	if f.http != nil {
		_ = f.http.Close()
		<-f.serveDone
		f.http = nil
	}
}

type command struct {
	Op        string `json:"op"`
	AID       string `json:"aid"`
	PIN       string `json:"pin"`
	PublicKey string `json:"public_key"`
}

func (f *fixture) command(ctx context.Context, c command) (any, error) {
	switch c.Op {
	case "begin":
		return nil, f.manager.PairBegin()
	case "cancel":
		return nil, f.manager.PairCancel()
	case "pending":
		err := waitFor(ctx, func() (bool, error) { p, err := f.manager.PairPending(); return p.Phase == relayproto.PairProved, err })
		if err != nil {
			return nil, err
		}
		return f.manager.PairPending()
	case "accept", "accept-drop":
		aid, err := base64.RawURLEncoding.DecodeString(c.AID)
		if err != nil {
			return nil, err
		}
		pk, err := base64.RawURLEncoding.DecodeString(c.PublicKey)
		if err != nil {
			return nil, err
		}
		var dropped chan struct{}
		if c.Op == "accept-drop" {
			dropped = make(chan struct{})
			f.mu.Lock()
			f.drop = dropped
			f.mu.Unlock()
		}
		device, err := f.manager.PairAccept(aid, c.PIN, pk, "native fixture")
		if err != nil {
			return nil, err
		}
		if dropped != nil {
			select {
			case <-dropped:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return map[string]any{"ui_id": device.UIID, "generation": device.Generation}, nil
	case "restart":
		f.stop()
		if err := f.start(ctx, false); err != nil {
			return nil, err
		}
		return f.info()
	case "info":
		return f.info()
	default:
		return nil, errors.New("unknown fixture operation")
	}
}

func (f *fixture) info() (any, error) {
	state, err := f.store.Snapshot()
	if err != nil {
		return nil, err
	}
	return map[string]any{"origin": f.origin, "host_id": state.HostID, "host_fp": state.HostFP, "admission": admission, "devices": len(state.Devices)}, nil
}

func run() error {
	dir, err := os.MkdirTemp("", "omo-native-relay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err = os.WriteFile(filepath.Join(dir, "password"), []byte(admission), 0600); err != nil {
		return err
	}
	f := &fixture{dir: dir}
	defer f.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = f.start(ctx, true)
	cancel()
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	info, err := f.info()
	if err != nil {
		return err
	}
	if err = encoder.Encode(map[string]any{"ok": true, "data": info}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		var c command
		if err = json.Unmarshal(scanner.Bytes(), &c); err != nil {
			return err
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		data, opErr := f.command(ctx, c)
		cancel()
		response := map[string]any{"ok": opErr == nil, "data": data}
		if opErr != nil {
			response["error"] = opErr.Error()
		}
		if err = encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
