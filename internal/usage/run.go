package usage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/luabox"
)

// DefaultRunTimeout bounds one run of a script, helpers included.
const DefaultRunTimeout = 60 * time.Second

// RunOptions configure the runner. Zero fields take the defaults.
type RunOptions struct {
	// HTTPClient is the client of omo.http; nil: a fresh one per run.
	// Tests inject one whose transport answers from a fake server.
	HTTPClient *http.Client
	// ReadFile reads a file for omo.read_file; nil: homefile.Read (a
	// regular file, no link at the last element, bounded).
	ReadFile func(path string, max int64) ([]byte, error)
	// Keychain reads a macOS keychain entry; nil: /usr/bin/security on
	// darwin, "not available" elsewhere.
	Keychain func(ctx context.Context, service string) (string, error)
	// RefreshCodex lets the Codex CLI refresh its credentials; nil:
	// codexauth.Refresher.
	RefreshCodex func(ctx context.Context, command string, env []string, authPath string) error
	// Home is the user's home folder; "" asks the operating system.
	Home string
	// Timeout bounds the whole run; 0 = 60s.
	Timeout time.Duration
}

// goos is the operating system a script is told it runs on. Tests change it.
var goos = runtime.GOOS

// NewRunFunc returns a RunFunc that runs every job with o.
func NewRunFunc(o RunOptions) RunFunc {
	return func(ctx context.Context, j Job) (Result, error) { return Run(ctx, j, o) }
}

// Run runs one job in a fresh box and validates its result. The run ends
// when ctx ends or after o.Timeout. Its error text is one line, at most 300
// characters, with every secret the run touched replaced; the log is cleaned
// the same way. A failed run still returns the log it wrote.
func Run(ctx context.Context, j Job, o RunOptions) (Result, error) {
	if o.Timeout <= 0 {
		o.Timeout = DefaultRunTimeout
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	box := luabox.New(ctx, luabox.Limits{Timeout: o.Timeout})
	defer box.Close()
	h := newHelpers(box, j, o)
	h.register()
	if err := box.Load(j.Text, chunkName(j.Script)); err != nil {
		return Result{Log: box.Log()}, scriptError(err, box)
	}
	out, err := box.Call("usage", h.account())
	if err != nil {
		if h.limited {
			return Result{Log: box.Log()}, &RateLimitError{RetryAfter: h.retryAfter}
		}
		return Result{Log: box.Log()}, scriptError(err, box)
	}
	ws, err := validateResult(out, box.Redactor())
	if err != nil {
		return Result{Log: box.Log()}, scriptError(err, box)
	}
	return Result{Windows: ws, Log: box.Log()}, nil
}

// chunkName is the name a script's source is loaded under.
func chunkName(script string) string {
	if script == "" {
		return "script"
	}
	return script
}

// scriptError is the error a RunFunc returns: luabox.ErrorText of err.
func scriptError(err error, box *luabox.Box) error {
	return errors.New(luabox.ErrorText(err, box.Redactor()))
}

// Bounds of a run's result.
const (
	// maxUnixSeconds is the largest Unix time resets_at may be (year 5138).
	maxUnixSeconds = 1e11
)

var windowFields = map[string]bool{"name": true, "label": true, "used": true, "resets_at": true}

// validateResult checks what usage returned against the contract: a list
// of at most MaxWindows tables {name, label?, used, resets_at?}. A label is
// made one short line (cleanLabel); one that holds a secret of the run is
// refused, as a name is. Errors name the window by its place, never a value.
func validateResult(v any, red *luabox.Redactor) ([]Window, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("usage must return a list of windows")
	}
	if len(list) > MaxWindows {
		return nil, fmt.Errorf("usage returned more than %d windows", MaxWindows)
	}
	out := make([]Window, 0, len(list))
	seen := map[string]bool{}
	for i, item := range list {
		n := i + 1
		t, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("window %d: must be a table with name and used", n)
		}
		for k := range t {
			if !windowFields[k] {
				return nil, fmt.Errorf("window %d: has a field other than name, label, used and resets_at", n)
			}
		}
		name, ok := t["name"].(string)
		if !ok || !windowName.MatchString(name) {
			return nil, fmt.Errorf("window %d: the name must be 1 to 40 lowercase letters, digits, - or _, starting with a letter or digit", n)
		}
		if red.Clean(name) != name {
			return nil, fmt.Errorf("window %d: the name holds a secret", n)
		}
		if seen[name] {
			return nil, fmt.Errorf("window %d: the name is used twice", n)
		}
		seen[name] = true
		used, ok := t["used"].(float64)
		if !ok || math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
			return nil, fmt.Errorf("window %d: used must be a number from 0 to 100", n)
		}
		w := Window{Name: name, UsedPercent: min(used, 100)}
		switch label := t["label"].(type) {
		case nil:
		case string:
			whole := cleanLabel(label, -1)
			if red.Clean(whole) != whole || strings.Count(red.Clean(label), luabox.Redacted) > strings.Count(label, luabox.Redacted) {
				return nil, fmt.Errorf("window %d: the label holds a secret", n)
			}
			w.Label = cleanLabel(whole, MaxLabel)
		default:
			return nil, fmt.Errorf("window %d: the label must be text", n)
		}
		switch at := t["resets_at"].(type) {
		case nil:
		case string:
			parsed, err := time.Parse(time.RFC3339, at)
			if err != nil {
				return nil, fmt.Errorf("window %d: resets_at must be RFC 3339 text or Unix seconds", n)
			}
			w.ResetsAt = parsed.UTC()
		case float64:
			if math.IsNaN(at) || at <= 0 || at > maxUnixSeconds {
				return nil, fmt.Errorf("window %d: resets_at must be RFC 3339 text or Unix seconds", n)
			}
			sec, frac := math.Modf(at)
			w.ResetsAt = time.Unix(int64(sec), int64(frac*1e9)).UTC()
		default:
			return nil, fmt.Errorf("window %d: resets_at must be RFC 3339 text or Unix seconds", n)
		}
		out = append(out, w)
	}
	return out, nil
}
