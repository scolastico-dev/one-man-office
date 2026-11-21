package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxMessage = 16 << 20
const requestTimeout = 30 * time.Second

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("LSP %d: %s", e.Code, e.Message) }

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func readMessage(r *bufio.Reader) (message, error) {
	length := -1
	total := 0
	for {
		fragment, err := r.ReadSlice('\n')
		line := string(fragment)
		if err != nil {
			return message{}, err
		}
		total += len(line)
		if total > 8192 {
			return message{}, errors.New("LSP header too large")
		}
		if line == "\r\n" {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			return message{}, errors.New("invalid LSP header")
		}
		if strings.EqualFold(key, "Content-Length") {
			if length != -1 {
				return message{}, errors.New("duplicate LSP length")
			}
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 || n > maxMessage {
				return message{}, errors.New("invalid LSP length")
			}
			length = n
		}
	}
	if length < 0 {
		return message{}, errors.New("missing LSP length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return message{}, err
	}
	var m message
	err := json.Unmarshal(body, &m)
	return m, err
}
func writeMessage(w io.Writer, m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > maxMessage {
		return errors.New("LSP message too large")
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

type peer struct {
	in      io.ReadCloser
	out     io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	next    int
	pending map[string]chan message
	done    chan struct{}
	handler func(string, json.RawMessage) (any, error)
	err     error
}

func newPeer(in io.ReadCloser, out io.WriteCloser, handler func(string, json.RawMessage) (any, error)) *peer {
	p := &peer{in: in, out: out, pending: map[string]chan message{}, done: make(chan struct{}), handler: handler}
	go p.read()
	return p
}
func (p *peer) write(m message) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	m.JSONRPC = "2.0"
	return writeMessage(p.out, m)
}

// A stopped or non-reading server must not trap the worktree worker in a
// pipe write. Closing both pipes on cancellation also releases server requests
// waiting for the writer mutex; the child is reaped by the runtime.
func (p *peer) sendContext(ctx context.Context, m message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() { result <- p.write(m) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		_ = p.in.Close()
		_ = p.out.Close()
		<-result
		return ctx.Err()
	}
}
func (p *peer) send(m message) error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	return p.sendContext(ctx, m)
}
func (p *peer) notify(method string, params any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return p.send(message{Method: method, Params: b})
}
func (p *peer) call(ctx context.Context, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.next++
	id := strconv.Itoa(p.next)
	ch := make(chan message, 1)
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	if err = p.sendContext(ctx, message{ID: json.RawMessage(id), Method: method, Params: b}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.err
	}
}
func (p *peer) read() {
	defer close(p.done)
	r := bufio.NewReader(p.in)
	for {
		m, err := readMessage(r)
		if err != nil {
			p.mu.Lock()
			p.err = err
			p.mu.Unlock()
			return
		}
		if m.Method == "" {
			p.mu.Lock()
			ch := p.pending[string(m.ID)]
			p.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
			continue
		}
		var result any
		if p.handler != nil {
			result, err = p.handler(m.Method, m.Params)
		} else {
			err = &rpcError{-32601, "method not supported"}
		}
		if len(m.ID) > 0 {
			response := message{ID: m.ID}
			if err != nil {
				response.Error = &rpcError{-32603, err.Error()}
				var rpc *rpcError
				if errors.As(err, &rpc) {
					response.Error = rpc
				}
			} else {
				response.Result, _ = json.Marshal(result)
			}
			if err := p.send(response); err != nil {
				p.mu.Lock()
				p.err = err
				p.mu.Unlock()
				return
			}
		}
	}
}
func (p *peer) close() { _ = p.in.Close(); _ = p.out.Close(); <-p.done }
