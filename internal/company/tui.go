package company

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

type tuiRequest struct {
	Agent string `json:"agent"`
}

func (s *Server) instanceTUI(w http.ResponseWriter, r *http.Request) {
	var request tuiRequest
	if err := decode(w, r, &request); err != nil {
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
	err = sockc.CallTimeout(endpoint, "user", "tui.show", proto.TUIShowArgs{Agent: request.Agent}, nil, 3*time.Second)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "deadline") {
			status = http.StatusGatewayTimeout
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusOK)
}
