// Package apiclient is the client of the local omo socket used by `omo ai`.
package apiclient

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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

// SessionHeader carries the agent session id.
const SessionHeader = "X-Omo-Session"

// ErrNotRunning is returned when no omo server answers on the socket.
var ErrNotRunning = errors.New("omo is not running (start it with: omo)")

// probeTimeout bounds the reachability check of the socket.
const probeTimeout = 3 * time.Second

// maxResponse bounds how much of a response body is read.
const maxResponse = 64 << 20

// Client talks to the omo server over its local socket.
type Client struct {
	http     *http.Client
	endpoint string
	session  string

	mu   sync.Mutex
	last []byte
}

// New returns a client for the socket at endpoint. A non-empty session is
// sent as X-Omo-Session; without it the caller is the user.
func New(endpoint, session string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return transport.Dial(ctx, endpoint)
		},
	}
	return &Client{http: &http.Client{Transport: tr}, endpoint: endpoint, session: session}
}

// ErrEmptyAgentID is returned when OMO_AGENT_ID is set but empty: the
// process was meant to be an agent and must not silently act as the user.
var ErrEmptyAgentID = errors.New("OMO_AGENT_ID is set but empty: this agent terminal is broken; ask your supervisor or the user")

// FromEnvironment builds a client from OMO_SOCKET (else the socket of the
// omo home) and OMO_AGENT_ID. An unset OMO_AGENT_ID is the user; one that is
// set but empty or blank is ErrEmptyAgentID and nothing is sent. It never
// starts a server; when nothing answers it returns ErrNotRunning.
func FromEnvironment() (*Client, error) {
	session, isAgent := os.LookupEnv("OMO_AGENT_ID")
	if isAgent {
		session = strings.TrimSpace(session)
		if session == "" {
			return nil, ErrEmptyAgentID
		}
	}
	endpoint := os.Getenv("OMO_SOCKET")
	if endpoint == "" {
		root, err := globalhome.Dir()
		if err != nil {
			return nil, err
		}
		endpoint, err = transport.ClientEndpoint(filepath.Join(root, "run"))
		if err != nil {
			return nil, ErrNotRunning
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	conn, err := transport.Dial(ctx, endpoint)
	if err != nil {
		return nil, ErrNotRunning
	}
	conn.Close()
	return New(endpoint, session), nil
}

// LastBody returns the body of the most recent successful response.
func (c *Client) LastBody() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Do sends a request with body (JSON encoded, may be nil) and decodes the
// response into out (may be nil). A non-2xx answer is returned as *api.Error.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://omo"+path, reader)
	if err != nil {
		return err
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(ctx, req, out)
}

// send sends req with the session header and decodes the answer as Do does.
func (c *Client) send(ctx context.Context, req *http.Request, out any) error {
	if c.session != "" {
		req.Header.Set(SessionHeader, c.session)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return ErrNotRunning
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("cannot reach omo: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return decodeError(resp.StatusCode, data)
	}
	c.mu.Lock()
	c.last = data
	c.mu.Unlock()
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func decodeError(status int, data []byte) error {
	var env api.ErrorEnvelope
	if json.Unmarshal(data, &env) == nil && env.Error != nil && env.Error.Message != "" {
		env.Error.Status = status
		return env.Error
	}
	msg := strings.TrimSpace(string(data))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &api.Error{Status: status, Code: api.CodeInternal, Message: msg}
}

func ticketPath(n int, rest string) string { return "/api/tickets/" + strconv.Itoa(n) + rest }

// Me returns the caller's identity and own ticket.
func (c *Client) Me(ctx context.Context) (api.Me, error) {
	var out api.Me
	return out, c.Do(ctx, http.MethodGet, "/api/me", nil, &out)
}

// Ticket returns one ticket with its description.
func (c *Client) Ticket(ctx context.Context, n int) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodGet, ticketPath(n, ""), nil, &out)
}

// Tickets lists the tickets matching f.
func (c *Client) Tickets(ctx context.Context, f api.TicketFilter) (api.TicketList, error) {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("column", string(f.Column))
	set("type", string(f.Type))
	set("project", f.Project)
	set("supervisor", string(f.Supervisor))
	set("q", f.Search)
	if f.Story != nil {
		q.Set("story", strconv.Itoa(*f.Story))
	}
	if f.NeedsMe {
		q.Set("needs_me", "true")
	}
	if f.DoneLimit > 0 {
		q.Set("done_limit", strconv.Itoa(f.DoneLimit))
	}
	if f.DoneBefore > 0 {
		q.Set("done_before", strconv.Itoa(f.DoneBefore))
	}
	path := "/api/tickets"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out api.TicketList
	return out, c.Do(ctx, http.MethodGet, path, nil, &out)
}

// CreateTicket creates a ticket.
func (c *Client) CreateTicket(ctx context.Context, req api.CreateTicket) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodPost, "/api/tickets", req, &out)
}

// UpdateTicket patches the fields set in req.
func (c *Client) UpdateTicket(ctx context.Context, n int, req api.UpdateTicket) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodPatch, ticketPath(n, ""), req, &out)
}

// SetDescription replaces the description of ticket n.
func (c *Client) SetDescription(ctx context.Context, n int, req api.DescriptionUpdate) (api.DescriptionVersion, error) {
	var out api.DescriptionVersion
	return out, c.Do(ctx, http.MethodPut, ticketPath(n, "/description"), req, &out)
}

// Move moves ticket n to another column.
func (c *Client) Move(ctx context.Context, n int, req api.MoveRequest) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodPost, ticketPath(n, "/move"), req, &out)
}

// Cancel closes ticket n as cancelled.
func (c *Client) Cancel(ctx context.Context, n int, req api.CancelRequest) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodPost, ticketPath(n, "/cancel"), req, &out)
}

// RemoveProject takes a project off ticket n with the worktree and branch
// omo made for it there.
func (c *Client) RemoveProject(ctx context.Context, n int, req api.RemoveProjectRequest) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodPost, ticketPath(n, "/remove-project"), req, &out)
}

// AddDependency makes ticket n depend on m.
func (c *Client) AddDependency(ctx context.Context, n, m int) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodPost, ticketPath(n, "/dependencies"), api.DependencyRequest{DependsOn: m}, &out)
}

// RemoveDependency removes the dependency of n on m.
func (c *Client) RemoveDependency(ctx context.Context, n, m int) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodDelete, ticketPath(n, "/dependencies/"+strconv.Itoa(m)), nil, &out)
}

// Comments returns the comment thread of ticket n, oldest first.
func (c *Client) Comments(ctx context.Context, n int) ([]api.Comment, error) {
	var out []api.Comment
	return out, c.Do(ctx, http.MethodGet, ticketPath(n, "/comments"), nil, &out)
}

// Comment posts a comment or question on ticket n.
func (c *Client) Comment(ctx context.Context, n int, req api.CommentRequest) (api.CommentResult, error) {
	var out api.CommentResult
	return out, c.Do(ctx, http.MethodPost, ticketPath(n, "/comments"), req, &out)
}

// Done reports the caller's work as finished (or a reviewer's verdict).
func (c *Client) Done(ctx context.Context, req api.DoneRequest) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodPost, "/api/ai/done", req, &out)
}

// ExportText has omo check the pull request text the calling export job
// wrote and answers what the pull request gets.
func (c *Client) ExportText(ctx context.Context) (api.ExportText, error) {
	var out api.ExportText
	return out, c.Do(ctx, http.MethodPost, "/api/ai/export-text", struct{}{}, &out)
}

// Projects lists the registered projects matching q.
func (c *Client) Projects(ctx context.Context, q string, all bool) ([]api.Project, error) {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if all {
		v.Set("all", "true")
	}
	path := "/api/projects"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	var out []api.Project
	return out, c.Do(ctx, http.MethodGet, path, nil, &out)
}

// Models lists the models the caller may select.
func (c *Client) Models(ctx context.Context) ([]api.Model, error) {
	var out []api.Model
	return out, c.Do(ctx, http.MethodGet, "/api/models", nil, &out)
}

// Ready announces that the agent is ready for work and returns its prompt.
func (c *Client) Ready(ctx context.Context) (string, error) {
	var out struct {
		Prompt string `json:"prompt"`
	}
	return out.Prompt, c.Do(ctx, http.MethodPost, "/api/ai/ready", struct{}{}, &out)
}

// Wait blocks until the server has something for the agent or timeout (a Go
// duration such as "10m") passes, and returns the reason it woke up.
func (c *Client) Wait(ctx context.Context, timeout string) (string, error) {
	d, err := time.ParseDuration(strings.TrimSpace(timeout))
	if err != nil {
		return "", fmt.Errorf("timeout %q is not a duration: %w", timeout, err)
	}
	var out api.WaitResponse
	err = c.Do(ctx, http.MethodPost, "/api/ai/wait", api.WaitRequest{Timeout: api.Duration(d)}, &out)
	return out.Reason, err
}

// Retry asks the board to retry the failed step of ticket n.
func (c *Client) Retry(ctx context.Context, n int) (api.Ticket, error) {
	var out api.Ticket
	return out, c.Do(ctx, http.MethodPost, ticketPath(n, "/retry"), struct{}{}, &out)
}

// Scheduler returns the engine's current load.
func (c *Client) Scheduler(ctx context.Context) (api.SchedulerState, error) {
	var out api.SchedulerState
	return out, c.Do(ctx, http.MethodGet, "/api/scheduler", nil, &out)
}

// ImageNameHeader carries the percent-encoded original name of an upload.
const ImageNameHeader = "Omo-Image-Name"

// UploadImage sends the bytes of an image file to ticket n under its
// original file name; the server decides from the bytes what it is.
func (c *Client) UploadImage(ctx context.Context, n int, name string, data []byte) (api.Image, error) {
	var out api.Image
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://omo"+ticketPath(n, "/images"), bytes.NewReader(data))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(ImageNameHeader, url.PathEscape(name))
	err = c.send(ctx, req, &out)
	return out, err
}

// Image reads the record of an uploaded image, with its local path while
// its file is stored.
func (c *Client) Image(ctx context.Context, id string) (api.Image, error) {
	var out api.Image
	err := c.Do(ctx, http.MethodGet, "/api/images/"+url.PathEscape(id), nil, &out)
	return out, err
}
