package websupervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const maxCommandRequestBytes = 64 * 1024

type executeRequest struct {
	Cwd     string   `json:"cwd,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type commandEvent struct {
	Type   string `json:"type"`
	Stream string `json:"stream,omitempty"`
	Data   string `json:"data,omitempty"`
	Code   int    `json:"code"`
	Error  string `json:"error,omitempty"`
}

type commandStream struct {
	mu      *sync.Mutex
	encoder *json.Encoder
	flusher http.Flusher
	stream  string
}

func (s *commandStream) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.encoder.Encode(commandEvent{Type: "output", Stream: s.stream, Data: string(data)}); err != nil {
		return 0, err
	}
	s.flusher.Flush()
	return len(data), nil
}

func (s *commandStream) finish(event commandEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.encoder.Encode(event)
	s.flusher.Flush()
	return err
}

func (s *Server) execute(w http.ResponseWriter, r *http.Request) {
	select {
	case s.commands <- struct{}{}:
		defer func() { <-s.commands }()
	default:
		http.Error(w, "too many commands are running", http.StatusTooManyRequests)
		return
	}
	var request executeRequest
	if err := decodeLimit(w, r, &request, maxCommandRequestBytes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(request.Command) == "" || strings.ContainsRune(request.Command, 0) || len(request.Args) > 128 {
		http.Error(w, "command must be non-empty and contain at most 128 arguments", http.StatusBadRequest)
		return
	}
	for _, arg := range request.Args {
		if strings.ContainsRune(arg, 0) {
			http.Error(w, "arguments cannot contain NUL", http.StatusBadRequest)
			return
		}
	}
	dir, err := commandDirectory(request.Cwd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cmd := exec.CommandContext(r.Context(), request.Command, request.Args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OMO_SUPERVISOR=1")
	cmd.WaitDelay = time.Second
	lifecycle, err := configureCommandCancellation(cmd)
	if err != nil {
		http.Error(w, fmt.Sprintf("prepare command: %v", err), http.StatusInternalServerError)
		return
	}
	defer lifecycle.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	encoder := json.NewEncoder(w)
	streamMu := &sync.Mutex{}
	stdout := &commandStream{encoder: encoder, flusher: flusher, stream: "stdout", mu: streamMu}
	stderr := &commandStream{encoder: encoder, flusher: flusher, stream: "stderr", mu: streamMu}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, fmt.Sprintf("open command stdout: %v", err), http.StatusInternalServerError)
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		http.Error(w, fmt.Sprintf("open command stderr: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := cmd.Start(); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Error(w, fmt.Sprintf("start command: %v", err), http.StatusBadRequest)
		return
	}
	if err := lifecycle.Started(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Error(w, fmt.Sprintf("contain command: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	var copies sync.WaitGroup
	copies.Go(func() { _, _ = io.Copy(stdout, stdoutPipe) })
	copies.Go(func() { _, _ = io.Copy(stderr, stderrPipe) })
	err = cmd.Wait()
	copies.Wait()
	event := commandEvent{Type: "exit"}
	if err != nil {
		event.Code = -1
		event.Error = err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			event.Code = exit.ExitCode()
		}
	}
	_ = stdout.finish(event)
}

func commandDirectory(cwd string) (string, error) {
	if cwd == "" || cwd == "home" {
		return shellDirectory("")
	}
	return TrustedProject(cwd)
}

func decodeLimit(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	if content := r.Header.Get("Content-Type"); !strings.HasPrefix(content, "application/json") {
		return fmt.Errorf("application/json required")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid trailing request data")
	}
	return nil
}
