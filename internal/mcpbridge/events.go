package mcpbridge

import (
	"bufio"
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// maxRetryWait bounds the pause between two attempts to open the event
// stream.
const maxRetryWait = 10 * time.Second

// watchEvents follows omo's event stream until ctx ends and calls changed
// on every mcp.tools_changed event, and once more each time the stream
// comes back after it was lost: omo restarted, and what it offers may
// differ.
//
// The stream is read without a session header: it is the user's, refused
// to agent sessions. The bridge reads nothing from it but the kind of each
// event and passes nothing of it on.
func watchEvents(ctx context.Context, dial func(context.Context) (net.Conn, error), changed func(), logger *log.Logger) {
	hc := &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		DisableKeepAlives: true,
	}}
	wait := retryWait
	before := false // the stream was open before
	for ctx.Err() == nil {
		if followEvents(ctx, hc, func() {
			if before {
				changed()
			}
			before = true
			wait = retryWait
		}, changed) && ctx.Err() == nil {
			logger.Printf("omo's event stream ended; opening it again")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(2*wait, maxRetryWait)
	}
}

// followEvents reads the event stream once. opened runs when omo answered;
// changed runs for each mcp.tools_changed event. It reports whether the
// stream was opened.
func followEvents(ctx context.Context, hc *http.Client, opened, changed func()) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://omo/api/events", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	opened()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		if kind, ok := strings.CutPrefix(sc.Text(), "event: "); ok && kind == api.KindMCPToolsChanged {
			changed()
		}
	}
	return true
}
