package websupervisor

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"github.com/scolastico-dev/one-man-office/internal/websupervisor/controlplane"
)

//go:embed assets/*
var assets embed.FS

type Options struct {
	Listen    string
	MaxAgents int
	UsageTTL  time.Duration
	Mock      bool
}

type Server struct {
	options     Options
	token       string
	authority   string
	exposed     bool
	control     *controlplane.Server
	controlHTTP *http.Server
	controlURL  string
	mu          sync.Mutex
	instances   map[string]*Instance
	closed      bool
	closeOnce   sync.Once
	ctx         context.Context
	cancel      context.CancelFunc
	connections chan struct{}
}

// New starts the private child listener. The caller must Close the server.
func New(options Options) (*Server, error) {
	if options.MaxAgents < 1 {
		return nil, fmt.Errorf("max-agents must be positive")
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	if _, err := Projects(); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{options: options, token: token, control: controlplane.New(options.MaxAgents, nil, options.UsageTTL), controlURL: "http://" + listener.Addr().String(), instances: map[string]*Instance{}, ctx: ctx, cancel: cancel, connections: make(chan struct{}, 16)}
	s.controlHTTP = &http.Server{Handler: s.control.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { _ = s.controlHTTP.Serve(listener) }()
	return s, nil
}

func randomToken() (string, error) {
	var data [32]byte
	_, err := rand.Read(data[:])
	return hex.EncodeToString(data[:]), err
}

// Run serves until cancellation and then waits for owned children to stop.
func Run(ctx context.Context, options Options, out io.Writer) error {
	if options.Listen == "" {
		options.Listen = "127.0.0.1:8090"
	}
	if options.UsageTTL <= 0 {
		options.UsageTTL = modelusage.DefaultCacheTTL
	}
	listener, err := net.Listen("tcp", options.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	s, err := New(options)
	if err != nil {
		return err
	}
	defer s.Close()
	s.authority = listener.Addr().String()
	host, _, _ := net.SplitHostPort(s.authority)
	s.exposed = !net.ParseIP(host).IsLoopback()
	if s.exposed {
		fmt.Fprintln(out, "WARNING: supervisor is exposed beyond loopback over plain HTTP. Anyone with its access URL can run commands with your user permissions. Prefer loopback with an SSH port-forward using the same local and remote port.")
	}
	fmt.Fprintf(out, "omo supervisor: http://%s/#%s\n", s.authority, s.token)
	httpServer := &http.Server{
		Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
		BaseContext: func(net.Listener) context.Context { return s.ctx },
	}
	finished := make(chan error, 1)
	go func() { finished <- httpServer.Serve(listener) }()
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	s.cancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("POST /api/projects", s.projectAction)
	mux.HandleFunc("POST /api/instances", s.launch)
	mux.HandleFunc("POST /api/instances/{id}/estop", s.estop)
	mux.HandleFunc("POST /api/instances/{id}/kill", s.kill)
	mux.HandleFunc("DELETE /api/instances/{id}", s.forget)
	mux.HandleFunc("GET /api/instances/{id}/terminal", s.terminal)
	files, _ := fs.Sub(assets, "assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(files))))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, files, "index.html") })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if !s.allowedHost(r.Host) || !sameOrigin(r) {
			http.Error(w, "host or origin rejected", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.authorized(r) {
			http.Error(w, "access URL required", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) allowedHost(authority string) bool {
	if authority == s.authority {
		return true
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return false
	}
	bound, boundPort, _ := net.SplitHostPort(s.authority)
	if port != boundPort {
		return false
	}
	if net.ParseIP(bound).IsLoopback() {
		return host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
	}
	return s.exposed && net.ParseIP(host) != nil
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && u.Host == r.Host && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

func (s *Server) authorized(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if strings.HasSuffix(r.URL.Path, "/terminal") {
		for _, protocol := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
			if strings.HasPrefix(strings.TrimSpace(protocol), "omo-token.") {
				token = strings.TrimPrefix(strings.TrimSpace(protocol), "omo-token.")
			}
		}
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

func decode(w http.ResponseWriter, r *http.Request, value any) error {
	if content := r.Header.Get("Content-Type"); !strings.HasPrefix(content, "application/json") {
		return fmt.Errorf("application/json required")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid trailing request data")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	projects, err := Projects()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.mu.Lock()
	instances := make([]InstanceInfo, 0, len(s.instances))
	runningOffices := make(map[string]struct{})
	for _, i := range s.instances {
		info := i.snapshot()
		instances = append(instances, info)
		if info.Mode == "omo" && info.State == "running" {
			runningOffices[info.Path] = struct{}{}
		}
	}
	s.mu.Unlock()
	launchable := projects[:0]
	for _, project := range projects {
		if _, running := runningOffices[project.Path]; !running {
			launchable = append(launchable, project)
		}
	}
	used, limit := s.control.Stats()
	writeJSON(w, 200, map[string]any{"projects": launchable, "instances": instances, "agents": used, "max_agents": limit})
}

func (s *Server) projectAction(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	if err := decode(w, r, &request); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var project Project
	var err error
	switch request.Action {
	case "trust":
		project, err = TrustProject(request.Path)
	case "create", "clone":
		if request.Action == "clone" && request.Source == "" {
			http.Error(w, "clone source required", 400)
			return
		}
		if request.Action == "create" && request.Source != "" {
			http.Error(w, "create cannot include source", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		project, err = CreateProject(ctx, request.Path, request.Source)
	default:
		http.Error(w, "unknown project action", 400)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 201, project)
}

func cleanEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "OMO_CONTROL_") || key == "OMO_AGENT_ID" || key == "OMO_SOCKET" || key == "TERM" || key == "COLORTERM" || key == "COLORFGBG" {
			continue
		}
		env = append(env, entry)
	}
	// These terminals can have several attached displays, like a multiplexer.
	// screen-256color also prevents Bubble Tea's package-init OSC color probe
	// from consuming early user keystrokes before the child starts reading.
	return append(env, "TERM=screen-256color", "COLORTERM=truecolor", "COLORFGBG=15;0")
}
