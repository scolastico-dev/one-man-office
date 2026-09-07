package websupervisor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func (s *Server) start(path, mode string) (*Instance, error) {
	if mode != "omo" && mode != "shell" {
		return nil, fmt.Errorf("mode must be omo or shell")
	}
	canonical, err := TrustedProject(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("supervisor is stopping")
	}
	if mode == "omo" {
		for _, i := range s.instances {
			info := i.snapshot()
			if info.Path == canonical && info.Mode == mode && info.State == "running" {
				return i, nil
			}
		}
	}
	if len(s.instances) >= 64 {
		return nil, fmt.Errorf("instance limit reached; remove exited terminals first")
	}
	if mode == "omo" {
		if endpoint, running, err := probeOffice(canonical); err != nil {
			return nil, err
		} else if running {
			if endpoint == "" {
				return nil, fmt.Errorf("office is starting; wait before launching it again")
			}
			return nil, fmt.Errorf("office is already running outside this supervisor")
		}
	}
	id, err := randomToken()
	if err != nil {
		return nil, err
	}
	var token string
	if mode == "omo" {
		token, err = s.control.Register(id, canonical)
	} else {
		token, err = s.control.RegisterShell(id)
	}
	if err != nil {
		return nil, err
	}
	binary, err := os.Executable()
	if err != nil {
		s.control.Unregister(token)
		return nil, err
	}
	var args []string
	if s.options.Mock {
		args = append(args, "--mock")
	}
	if mode == "shell" {
		args = []string{"supervisor-shell"}
	}
	env := append(cleanEnvironment(), "OMO_CONTROL_URL="+s.controlURL, "OMO_CONTROL_TOKEN="+token)
	i, err := startInstance(id, canonical, mode, binary, args, env, func() { s.control.Unregister(token) })
	if err != nil {
		s.control.Unregister(token)
		return nil, err
	}
	s.instances[id] = i
	return i, nil
}

func (s *Server) launch(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path      string `json:"path"`
		Mode      string `json:"mode"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := decode(w, r, &request); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if request.Mode == "omo" && !request.Confirmed {
		http.Error(w, "office launch confirmation required", 400)
		return
	}
	i, err := s.start(request.Path, request.Mode)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 201, i.snapshot())
}

func (s *Server) instance(w http.ResponseWriter, r *http.Request) *Instance {
	s.mu.Lock()
	i := s.instances[r.PathValue("id")]
	s.mu.Unlock()
	if i == nil {
		http.Error(w, "instance not found", 404)
	}
	return i
}

func estopInstance(i *Instance) error {
	info := i.snapshot()
	if info.Mode != "omo" {
		return fmt.Errorf("estop applies to offices; use kill for a shell")
	}
	if info.State != "running" {
		return fmt.Errorf("instance has exited")
	}
	endpoint, running, err := probeOffice(info.Path)
	if err != nil {
		return err
	}
	if !running || endpoint == "" {
		return fmt.Errorf("office socket is not ready; wait or use forced kill")
	}
	return sockc.CallTimeout(endpoint, "user", "office.estop", nil, nil, 3*time.Second)
}

// Unlike office.Running, dashboard observation must not remove lock state.
// Leave stale-lock reclamation and its startup grace to office.Open itself.
func probeOffice(dir string) (endpoint string, running bool, err error) {
	path := filepath.Join(dir, office.LockPath)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	endpoint = strings.TrimSpace(string(raw))
	if endpoint == "" {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return "", time.Since(info.ModTime()) < 2*time.Second, nil
	}
	return endpoint, sockc.Probe(endpoint, 250*time.Millisecond), nil
}

func (s *Server) estop(w http.ResponseWriter, r *http.Request) {
	i := s.instance(w, r)
	if i == nil {
		return
	}
	if err := estopInstance(i); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, 202, map[string]string{"status": "stop requested"})
}
func (s *Server) kill(w http.ResponseWriter, r *http.Request) {
	i := s.instance(w, r)
	if i == nil {
		return
	}
	if err := i.kill(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 202, map[string]string{"status": "kill requested"})
}
func (s *Server) forget(w http.ResponseWriter, r *http.Request) {
	i := s.instance(w, r)
	if i == nil {
		return
	}
	if i.snapshot().State != "exited" {
		http.Error(w, "stop the instance before removing it", 409)
		return
	}
	s.mu.Lock()
	delete(s.instances, r.PathValue("id"))
	s.mu.Unlock()
	w.WriteHeader(204)
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		s.closed = true
		instances := make([]*Instance, 0, len(s.instances))
		for _, i := range s.instances {
			instances = append(instances, i)
		}
		s.mu.Unlock()
		// Closing the private listener independently triggers fail-closed child
		// watchdogs, including shells. Try socket cleanup, then bound shutdown.
		_ = s.controlHTTP.Close()
		for _, i := range instances {
			go func() {
				if i.snapshot().Mode == "omo" {
					_ = estopInstance(i)
				} else {
					_ = i.kill()
				}
			}()
		}
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			for _, i := range instances {
				<-i.done
			}
			cancel()
		}()
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
		}
		for _, i := range instances {
			_ = i.kill()
		}
		forceDeadline := time.NewTimer(3 * time.Second)
		defer forceDeadline.Stop()
		for _, i := range instances {
			select {
			case <-i.done:
			case <-forceDeadline.C:
				return
			}
		}
	})
}
