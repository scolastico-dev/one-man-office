package company

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const maxCommandRequestBytes = 64 * 1024
const maxCommandStdinBytes int64 = 1 << 30

var errCommandStdinTooLarge = errors.New("stdin exceeds maximum size")

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
	s.executeWithStdinLimit(w, r, maxCommandStdinBytes)
}

func (s *Server) executeWithStdinLimit(w http.ResponseWriter, r *http.Request, stdinLimit int64) {
	select {
	case s.commands <- struct{}{}:
		defer func() { <-s.commands }()
	default:
		http.Error(w, "too many commands are running", http.StatusTooManyRequests)
		return
	}
	request, stdin, err := parseCommandRequest(w, r)
	if err != nil {
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
	commandContext, cancelCommand := context.WithCancel(r.Context())
	defer cancelCommand()
	cmd := exec.CommandContext(commandContext, request.Command, request.Args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OMO_COMPANY=1")
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
	responseController := http.NewResponseController(w)
	encoder := json.NewEncoder(w)
	streamMu := &sync.Mutex{}
	stdout := &commandStream{encoder: encoder, flusher: flusher, stream: "stdout", mu: streamMu}
	stderr := &commandStream{encoder: encoder, flusher: flusher, stream: "stderr", mu: streamMu}
	// Let os/exec own the output-copy goroutines. Waiting on StdoutPipe or
	// StderrPipe before draining them lets Wait close the pipes first and can
	// lose the child's final output chunk.
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	var stdinPipe io.WriteCloser
	if stdin != nil {
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			http.Error(w, fmt.Sprintf("open command stdin: %v", err), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The command can consume a multipart request while its output is streamed
	// back on the same HTTP/1 connection.
	_ = http.NewResponseController(w).EnableFullDuplex()
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
	streamMu.Lock()
	flusher.Flush()
	streamMu.Unlock()
	stdinDone := make(chan error, 1)
	if stdin != nil {
		go func() {
			_, copyErr := io.Copy(stdinPipe, &limitedCommandStdin{part: stdin.part, reader: stdin.multipart, limit: stdinLimit})
			_ = stdinPipe.Close()
			if copyErr != nil {
				_ = r.Body.Close()
				if errors.Is(copyErr, errCommandStdinTooLarge) || !isClosedCommandPipe(copyErr) {
					cancelCommand()
				}
			}
			stdinDone <- copyErr
		}()
	}
	err = cmd.Wait()
	var stdinErr error
	if stdin != nil {
		select {
		case stdinErr = <-stdinDone:
		default:
			// A child that exits without reading stdin can leave the request
			// reader blocked. Closing both ends releases it before waiting.
			_ = stdinPipe.Close()
			_ = responseController.SetReadDeadline(time.Now())
			_ = r.Body.Close()
			stdinErr = <-stdinDone
			_ = responseController.SetReadDeadline(time.Time{})
		}
	}
	event := commandEvent{Type: "exit"}
	if stdinErr != nil && (errors.Is(stdinErr, errCommandStdinTooLarge) || !isClosedCommandPipe(stdinErr)) {
		event.Code = -1
		event.Error = stdinErr.Error()
	}
	if err != nil {
		if event.Error == "" {
			event.Code = -1
			event.Error = err.Error()
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				event.Code = exit.ExitCode()
			}
		}
	}
	_ = stdout.finish(event)
}

type commandStdin struct {
	part      *multipart.Part
	multipart *multipart.Reader
}

func parseCommandRequest(w http.ResponseWriter, r *http.Request) (executeRequest, *commandStdin, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var request executeRequest
		if err := decodeLimit(w, r, &request, maxCommandRequestBytes); err != nil {
			return executeRequest{}, nil, err
		}
		return request, nil, nil
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		return executeRequest{}, nil, fmt.Errorf("application/json or multipart/form-data required")
	}
	multipartReader, err := r.MultipartReader()
	if err != nil {
		return executeRequest{}, nil, fmt.Errorf("invalid multipart request: %w", err)
	}
	part, err := multipartReader.NextPart()
	if errors.Is(err, io.EOF) {
		return executeRequest{}, nil, fmt.Errorf("multipart request must start with request part")
	}
	if err != nil {
		return executeRequest{}, nil, fmt.Errorf("read request part: %w", err)
	}
	if part.FormName() != "request" {
		return executeRequest{}, nil, fmt.Errorf("first multipart part must be request")
	}
	data, err := io.ReadAll(io.LimitReader(part, maxCommandRequestBytes+1))
	if err != nil {
		return executeRequest{}, nil, fmt.Errorf("read request part: %w", err)
	}
	if int64(len(data)) > maxCommandRequestBytes {
		return executeRequest{}, nil, fmt.Errorf("request part exceeds %d bytes", maxCommandRequestBytes)
	}
	var request executeRequest
	if err := decodeCommandJSON(bytes.NewReader(data), &request); err != nil {
		return executeRequest{}, nil, err
	}
	part, err = multipartReader.NextPart()
	if errors.Is(err, io.EOF) {
		return request, nil, nil
	}
	if err != nil {
		return executeRequest{}, nil, fmt.Errorf("read multipart parts: %w", err)
	}
	if part.FormName() != "stdin" {
		return executeRequest{}, nil, fmt.Errorf("second multipart part must be stdin")
	}
	return request, &commandStdin{part: part, multipart: multipartReader}, nil
}

func decodeCommandJSON(reader io.Reader, value any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid trailing request data")
	}
	return nil
}

type limitedCommandStdin struct {
	part   *multipart.Part
	reader *multipart.Reader
	limit  int64
	read   int64
}

func (r *limitedCommandStdin) Read(data []byte) (int, error) {
	if r.read >= r.limit {
		var extra [1]byte
		n, err := r.part.Read(extra[:])
		if n > 0 {
			return 0, errCommandStdinTooLarge
		}
		if err != nil {
			return 0, r.finish(err)
		}
		return 0, nil
	}
	remaining := r.limit - r.read
	readSize := int64(len(data))
	if readSize > remaining+1 {
		readSize = remaining + 1
	}
	n, err := r.part.Read(data[:readSize])
	if int64(n) > remaining {
		return int(remaining), errCommandStdinTooLarge
	}
	r.read += int64(n)
	if err != nil {
		return n, r.finish(err)
	}
	return n, nil
}

func (r *limitedCommandStdin) finish(err error) error {
	if !errors.Is(err, io.EOF) {
		return err
	}
	next, nextErr := r.reader.NextPart()
	if errors.Is(nextErr, io.EOF) {
		return io.EOF
	}
	if nextErr != nil {
		return fmt.Errorf("read multipart after stdin: %w", nextErr)
	}
	return fmt.Errorf("unexpected multipart part %q after stdin", next.FormName())
}

func isClosedCommandPipe(err error) bool {
	message := strings.ToLower(err.Error())
	var networkError net.Error
	return errors.Is(err, io.ErrClosedPipe) || strings.Contains(message, "broken pipe") || strings.Contains(message, "pipe is being closed") || strings.Contains(message, "file already closed") || strings.Contains(message, "invalid read on closed body") || (errors.As(err, &networkError) && networkError.Timeout())
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
	if err := decodeCommandJSON(http.MaxBytesReader(w, r.Body, limit), value); err != nil {
		return err
	}
	return nil
}
