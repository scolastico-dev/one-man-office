package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
)

var ErrLimit = errors.New("aggregate agent limit reached")

type Client struct {
	endpoint string
	token    string
	http     *http.Client
	mu       sync.Mutex
	failure  error
	provider SnapshotProvider
	notify   chan struct{}
}

type SnapshotProvider func() (LiveState, error)

// ClientFromEnv returns nil only for a standalone process. Partial or invalid
// supervision settings are fatal, never a reason to fall back to local calls.
func ClientFromEnv() (*Client, error) {
	endpoint, token := os.Getenv("OMO_CONTROL_URL"), os.Getenv("OMO_CONTROL_TOKEN")
	if endpoint == "" && token == "" {
		return nil, nil
	}
	return NewClient(endpoint, token)
}

func NewClient(endpoint, token string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid control endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || token == "" {
		return nil, fmt.Errorf("control endpoint requires loopback HTTP URL and token")
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), token: token, notify: make(chan struct{}, 1), http: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) fail(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure == nil {
		c.failure = fmt.Errorf("parent control plane lost: %w", err)
	}
	return c.failure
}

func (c *Client) call(ctx context.Context, path string, input request) (response, error) {
	c.mu.Lock()
	failure := c.failure
	c.mu.Unlock()
	if failure != nil {
		return response{}, failure
	}
	var bodyValue any = input
	if input.Live != nil {
		bodyValue = pingRequest{Agents: input.Live.Agents, TUI: input.Live.TUI, Actions: input.Live.Actions}
	}
	body, _ := json.Marshal(bodyValue)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return response{}, c.fail(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return response{}, c.fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return response{}, ErrLimit
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("control request %s failed (HTTP %d)", path, resp.StatusCode)
		if resp.StatusCode == http.StatusBadGateway || (resp.StatusCode == http.StatusForbidden && path == "/release") {
			return response{}, err
		}
		return response{}, c.fail(err)
	}
	var result response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return response{}, c.fail(err)
	}
	return result, nil
}

func (c *Client) SetSnapshotProvider(provider SnapshotProvider) {
	c.mu.Lock()
	c.provider = provider
	c.mu.Unlock()
}

func (c *Client) NotifyHeartbeat() {
	c.mu.Lock()
	notify := c.notify
	c.mu.Unlock()
	select {
	case notify <- struct{}{}:
	default:
	}
}

func (c *Client) Ping(ctx context.Context) error {
	c.mu.Lock()
	provider := c.provider
	c.mu.Unlock()
	var state *LiveState
	if provider != nil {
		if snapshot, err := provider(); err == nil {
			snapshot = normalizeLiveState(snapshot)
			state = &snapshot
		}
	}
	_, err := c.call(ctx, "/ping", request{Live: state})
	return err
}
func (c *Client) Acquire(ctx context.Context) (string, error) {
	r, err := c.call(ctx, "/acquire", request{})
	return r.Lease, err
}
func (c *Client) Release(ctx context.Context, lease string) error {
	_, err := c.call(ctx, "/release", request{Lease: lease})
	return err
}

// Profile contents never cross the trust boundary: the parent resolves only
// this registered key against the configuration it read at registration.
func (c *Client) Fetch(ctx context.Context, key string, _ config.Profile) (modelusage.Snapshot, error) {
	r, err := c.call(ctx, "/usage", request{Profile: key})
	return r.Snapshot, err
}
func (c *Client) Refresh(ctx context.Context, key string, p config.Profile) (modelusage.Snapshot, error) {
	return c.Fetch(ctx, key, p)
}

// Watch blocks until cancellation or parent loss and reports a loss once.
func (c *Client) Watch(ctx context.Context, onFailure func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var heartbeatTimer *time.Timer
	var heartbeat <-chan time.Time
	lastPing := time.Time{}
	ping := func() bool {
		if err := c.Ping(ctx); err != nil {
			if ctx.Err() == nil && onFailure != nil {
				onFailure(err)
			}
			return false
		}
		lastPing = time.Now()
		return true
	}
	if !ping() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			if heartbeatTimer != nil {
				heartbeatTimer.Stop()
			}
			return
		case <-ticker.C:
			wait := 500*time.Millisecond - time.Since(lastPing)
			if wait > 0 {
				if heartbeatTimer == nil {
					heartbeatTimer = time.NewTimer(wait)
					heartbeat = heartbeatTimer.C
				}
				continue
			}
			if !ping() {
				return
			}
		case <-c.notify:
			wait := 500*time.Millisecond - time.Since(lastPing)
			if wait <= 0 {
				if !ping() {
					return
				}
				continue
			}
			if heartbeatTimer == nil {
				heartbeatTimer = time.NewTimer(wait)
				heartbeat = heartbeatTimer.C
			}
		case <-heartbeat:
			heartbeatTimer = nil
			heartbeat = nil
			if !ping() {
				return
			}
		}
	}
}
