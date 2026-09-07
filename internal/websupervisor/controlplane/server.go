// Package controlplane coordinates capacity and usage for supervised offices.
// Its handler must be served only on a private loopback listener.
package controlplane

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
)

type child struct {
	id       string
	profiles map[string]config.Profile
	leases   map[string]bool
}

type Server struct {
	mu       sync.Mutex
	limit    int
	used     int
	children map[string]*child
	usage    *modelusage.Cache
}

func New(limit int, fetcher modelusage.Fetcher, ttl time.Duration) *Server {
	if fetcher == nil {
		fetcher = &modelusage.Client{}
	}
	return &Server{limit: limit, children: map[string]*child{}, usage: modelusage.NewCache(fetcher, ttl)}
}

// Register freezes the child's allowlist without modifying its configuration.
// An empty office directory registers a shell with heartbeat access only.
func (s *Server) Register(childID, officeDir string) (string, error) {
	var profiles map[string]config.Profile
	if officeDir != "" {
		absolute, err := filepath.Abs(officeDir)
		if err != nil {
			return "", err
		}
		officeDir = absolute
		cfg, err := config.LoadReadOnly(filepath.Join(officeDir, ".omo", "omo.yaml"))
		if err != nil {
			return "", err
		}
		profiles = cfg.Models
		// Provider credential roots are interpreted by the child relative
		// to its office cwd, which may differ from the dashboard's cwd.
		for key, profile := range profiles {
			if err := normalizeCredentialRoots(profile, officeDir, runtime.GOOS); err != nil {
				return "", fmt.Errorf("profile %q: %w", key, err)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if childID == "" {
		return "", fmt.Errorf("empty child ID")
	}
	for _, c := range s.children {
		if c.id == childID {
			return "", fmt.Errorf("child already registered")
		}
	}
	token, err := randomID()
	if err != nil {
		return "", err
	}
	s.children[token] = &child{id: childID, profiles: profiles, leases: map[string]bool{}}
	return token, nil
}

func (s *Server) RegisterShell(childID string) (string, error) { return s.Register(childID, "") }

// Unregister revokes authorization and releases all leases after child exit.
func (s *Server) Unregister(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.children[token]; c != nil {
		s.used -= len(c.leases)
		delete(s.children, token)
	}
}

func (s *Server) Stats() (used, limit int) { s.mu.Lock(); defer s.mu.Unlock(); return s.used, s.limit }

func randomID() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

type request struct {
	Profile string `json:"profile,omitempty"`
	Lease   string `json:"lease,omitempty"`
}
type response struct {
	Lease    string              `json:"lease,omitempty"`
	Snapshot modelusage.Snapshot `json:"snapshot,omitempty"`
}

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	c := s.children[token]
	if c == nil {
		s.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.Unlock()
	var req request
	if r.Body != nil && r.ContentLength != 0 {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid trailing data", http.StatusBadRequest)
			return
		}
	}
	// Never hold the aggregate lock while reading from a child. Recheck
	// registration after decoding because the process may have exited.
	s.mu.Lock()
	c = s.children[token]
	if c == nil {
		s.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/ping":
		s.mu.Unlock()
		writeJSON(w, response{})
	case "/acquire":
		if c.profiles == nil {
			s.mu.Unlock()
			http.Error(w, "shell cannot acquire agent leases", http.StatusForbidden)
			return
		}
		if s.limit <= 0 || s.used >= s.limit {
			s.mu.Unlock()
			http.Error(w, "aggregate agent limit reached", http.StatusTooManyRequests)
			return
		}
		lease, err := randomID()
		if err != nil {
			s.mu.Unlock()
			http.Error(w, "lease creation failed", http.StatusInternalServerError)
			return
		}
		c.leases[lease] = true
		s.used++
		s.mu.Unlock()
		writeJSON(w, response{Lease: lease})
	case "/release":
		if !c.leases[req.Lease] {
			s.mu.Unlock()
			http.Error(w, "unknown lease", http.StatusForbidden)
			return
		}
		delete(c.leases, req.Lease)
		s.used--
		s.mu.Unlock()
		writeJSON(w, response{})
	case "/usage":
		profile, ok := c.profiles[req.Profile]
		s.mu.Unlock()
		if !ok || !modelusage.Metered(profile) {
			http.Error(w, "profile not registered for usage", http.StatusForbidden)
			return
		}
		// The parent owns freshness. Child refresh timers share the same TTL,
		// rather than each bypassing the cache and multiplying provider calls.
		snapshot, err := s.usage.Fetch(r.Context(), req.Profile, profile)
		if err != nil {
			http.Error(w, "provider usage unavailable", http.StatusBadGateway)
			return
		}
		writeJSON(w, response{Snapshot: snapshot})
	default:
		s.mu.Unlock()
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
