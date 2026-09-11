package company

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

type pluginTriggerRequest struct {
	Plugin string   `json:"plugin,omitempty"`
	Action string   `json:"action"`
	Args   []string `json:"args"`
}

func validatePluginTriggerRequest(request pluginTriggerRequest, requirePlugin bool) error {
	if requirePlugin && !validPluginTriggerName(request.Plugin) {
		return fmt.Errorf("plugin must be a valid name")
	}
	if strings.TrimSpace(request.Action) == "" || strings.ContainsAny(request.Action, "\x00\r\n") {
		return fmt.Errorf("action must be non-empty and contain no control characters")
	}
	if len(request.Args) > 128 {
		return fmt.Errorf("args must contain at most 128 values")
	}
	for _, arg := range request.Args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("args cannot contain NUL")
		}
	}
	return nil
}

func validPluginTriggerName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\r\n") {
		return false
	}
	for index, r := range name {
		if index == 0 && !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) globalPluginTrigger(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validPluginTriggerName(name) {
		http.Error(w, "invalid plugin name", http.StatusBadRequest)
		return
	}
	var request pluginTriggerRequest
	if err := decode(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validatePluginTriggerRequest(request, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.plugins == nil {
		http.Error(w, "global plugins are unavailable", http.StatusInternalServerError)
		return
	}
	result, err := s.plugins.TriggerManualContextWithRoleResult(r.Context(), name, request.Action, "user", "user", request.Args)
	if err != nil {
		var permissionErr *plugins.ManualPermissionError
		if errors.As(err, &permissionErr) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, proto.PluginTriggerResponse{RequestID: result.RequestID, Result: result.Value})
}

func (s *Server) instancePluginTrigger(w http.ResponseWriter, r *http.Request) {
	var request pluginTriggerRequest
	if err := decode(w, r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validatePluginTriggerRequest(request, true); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	instance := s.instances[r.PathValue("id")]
	s.mu.Unlock()
	if instance == nil {
		http.Error(w, "instance is not available", http.StatusConflict)
		return
	}
	info := instance.snapshot()
	if info.Mode != "omo" || info.State != "running" {
		http.Error(w, "instance is not a running office", http.StatusConflict)
		return
	}
	endpoint, running, err := probeOffice(info.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("office socket is not ready: %v", err), http.StatusConflict)
		return
	}
	if !running || endpoint == "" {
		http.Error(w, "office socket is not ready", http.StatusConflict)
		return
	}
	var response proto.PluginTriggerResponse
	err = sockc.CallTimeout(endpoint, "user", "plugin.trigger", proto.PluginTriggerArgs{Name: request.Plugin, Action: request.Action, Args: request.Args, Async: true}, &response, 3*time.Second)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "deadline") {
			status = http.StatusGatewayTimeout
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request_id": response.RequestID})
}
