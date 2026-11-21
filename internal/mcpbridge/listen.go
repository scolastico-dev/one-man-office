package mcpbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A client of the bridge is a listener once it sent logging/setLevel: it
// asked for notifications/message, which is how omo reaches it. The bridge
// then holds omo's notification stream of its session (a GET of /mcp that
// accepts text/event-stream) and passes each notification on to the client.
// omo confirms a session as listening only while that stream is open, so a
// bridge whose client never asked, or that lost omo, leaves the agent to omo's
// chat delivery. Clients speaking the newest protocol version have no
// logging/setLevel and are not listeners through the bridge.

// logPassThrough is the level the bridge passes omo's notifications on at:
// the highest, so the level filter of the SDK cannot drop a notification omo
// counted as delivered.
const logPassThrough = "emergency"

// startListening starts the stream to omo once for the bridge's life.
func (b *bridge) startListening() {
	if b.o.Session == "" {
		return // omo has no session to notify
	}
	b.mu.Lock()
	started := b.listening
	b.listening = true
	b.mu.Unlock()
	if started {
		return
	}
	go b.up.listen(b.ctx, b.deliver, b.o.Log)
}

// deliver passes one of omo's notifications to the client.
func (b *bridge) deliver(ctx context.Context, text string) {
	b.mu.Lock()
	ss := b.session
	b.mu.Unlock()
	if ss != nil {
		_ = ss.Log(ctx, &mcp.LoggingMessageParams{Level: logPassThrough, Logger: "omo", Data: text})
	}
}

// listen holds omo's notification stream until ctx ends, opening it again
// after omo went away.
func (u *upstream) listen(ctx context.Context, deliver func(context.Context, string), logger interface{ Printf(string, ...any) }) {
	hc := &http.Client{Transport: &sessionTransport{session: u.session, base: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return u.dial(ctx) },
		DisableKeepAlives: true,
	}}}
	wait := retryWait
	for ctx.Err() == nil {
		opened := false
		if err := followNotifications(ctx, hc, func() { opened = true; wait = retryWait }, deliver); err != nil && ctx.Err() == nil && !opened {
			logger.Printf("omo's notification stream is not available: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(2*wait, maxRetryWait)
	}
}

// followNotifications reads the stream once; opened runs when omo accepted it.
func followNotifications(ctx context.Context, hc *http.Client, opened func(), deliver func(context.Context, string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &refusal{http.StatusText(resp.StatusCode)}
	}
	opened()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var n struct {
			Method string `json:"method"`
			Params struct {
				Data string `json:"data"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(data), &n) == nil && n.Method == "notifications/message" && n.Params.Data != "" {
			deliver(ctx, n.Params.Data)
		}
	}
	return sc.Err()
}
