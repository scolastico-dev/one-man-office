package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Bounds of what the tracker keeps and how it judges a reading.
const (
	// MaxWindows is the most windows one script run may report.
	MaxWindows = 32
	// MaxReadingAge is how long a successful reading counts. An older one
	// (kept over a long stop of omo, or after the clock jumped ahead)
	// leaves its models unknown until the next run.
	MaxReadingAge = 24 * time.Hour
	// MaxResetAhead is the furthest reset time taken from a script. A
	// window that says it resets later is kept without a reset time: it
	// counts until the next reading.
	MaxResetAhead = 31 * 24 * time.Hour
	// ResetDelay is how long after a window's reset time its account is
	// read again.
	ResetDelay = 30 * time.Second

	defaultSoftPercent = 90
	defaultHardPercent = 98
	defaultInterval    = 5 * time.Minute
	minInterval        = time.Minute
	maxInterval        = time.Hour
	maxErrorRunes      = 300
)

// retryAfter are the waits after the first, second and third failed run in a
// row; later failures wait the refresh interval.
var retryAfter = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute}

var windowName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// Evaluate judges one model against the reading of its account. windows are
// the window names that count for the model (its usage_windows); an empty
// list means every window of the reading. r is nil for a model whose profile
// has no usage script.
//
// The state is unmetered without a reading; unknown when the reading failed,
// never succeeded, is older than MaxReadingAge or holds no window that
// counts; otherwise hard when a counted window is at or over its hard
// threshold, else soft when one is at or over its soft threshold, else ok. A
// window whose reset time is at or before now counts as 0 %. The window
// reported is the one that decides the state: the most severe, then the
// fullest, then the first by name.
func Evaluate(model string, windows []string, r *api.UsageReading, th api.UsageSettings, now time.Time) api.ModelUsage {
	out := api.ModelUsage{Model: model, State: api.UsageUnmetered}
	if r == nil {
		return out
	}
	out.State = api.UsageUnknown
	if r.Error != "" || r.FetchedAt == nil || now.Sub(*r.FetchedAt) > MaxReadingAge {
		return out
	}
	best := -1
	var bestUsed float64
	var bestName string
	var bestReset *time.Time
	for _, w := range r.Windows {
		if len(windows) > 0 && !slices.Contains(windows, w.Name) {
			continue
		}
		used := w.UsedPercent
		if math.IsNaN(used) || math.IsInf(used, 0) {
			continue
		}
		used = min(max(used, 0), 100)
		reset := w.ResetsAt
		if reset != nil && !reset.After(now) {
			used, reset = 0, nil
		}
		soft, hard := Thresholds(w.Name, th)
		severity := 0
		switch {
		case used >= hard:
			severity = 2
		case used >= soft:
			severity = 1
		}
		if best < 0 || severity > best || (severity == best && (used > bestUsed || (used == bestUsed && w.Name < bestName))) {
			best, bestUsed, bestName, bestReset = severity, used, w.Name, reset
		}
	}
	if best < 0 {
		return out
	}
	out.State = []string{api.UsageOK, api.UsageSoft, api.UsageHard}[best]
	out.Window = bestName
	out.UsedPercent = &bestUsed
	if bestReset != nil {
		at := *bestReset
		out.ResetsAt = &at
	}
	return out
}

// Thresholds returns the soft and hard threshold of a window: its override
// where it sets one, else the general value. A value that is not a number
// above 0 and at most 100 is replaced by the general one, or by omo's
// default (90 and 98); a hard threshold below the soft one is the soft one.
func Thresholds(window string, th api.UsageSettings) (soft, hard float64) {
	soft = percentOr(th.SoftPercent, defaultSoftPercent)
	hard = percentOr(th.HardPercent, defaultHardPercent)
	if o, ok := th.Windows[window]; ok {
		if o.SoftPercent != nil {
			soft = percentOr(*o.SoftPercent, soft)
		}
		if o.HardPercent != nil {
			hard = percentOr(*o.HardPercent, hard)
		}
	}
	return soft, max(hard, soft)
}

func percentOr(v, fallback float64) float64 {
	if math.IsNaN(v) || v <= 0 || v > 100 {
		return fallback
	}
	return v
}

// refreshInterval is the settings' interval held to 1 minute to 1 hour; 0 or
// less is the default of 5 minutes.
func refreshInterval(th api.UsageSettings) time.Duration {
	d := time.Duration(th.RefreshInterval)
	if d <= 0 {
		return defaultInterval
	}
	return min(max(d, minInterval), maxInterval)
}

// nextDue is when an account is read again. last is when its last run ended
// and failures the failed runs in a row since its last success. After a
// success the next run is one interval later, or ResetDelay after the
// earliest reset time that lay ahead at the reading, whichever is first.
// After a failure it waits retryAfter (at most the interval). The result is
// never more than one interval after now, so a clock that went back does
// not postpone the run; one in the past means at once.
func nextDue(last time.Time, failures int, windows []api.UsageWindow, interval time.Duration, now time.Time) time.Time {
	next := last.Add(interval)
	switch {
	case failures > 0 && failures <= len(retryAfter):
		next = last.Add(min(retryAfter[failures-1], interval))
	case failures == 0:
		for _, w := range windows {
			if w.ResetsAt != nil && w.ResetsAt.After(last) {
				if at := w.ResetsAt.Add(ResetDelay); at.Before(next) {
					next = at
				}
			}
		}
	}
	if limit := now.Add(interval); next.After(limit) {
		next = limit
	}
	return next
}

// cleanWindows checks what a script reported and turns it into wire
// windows. A used percentage over 100 is 100; one below 0, NaN or infinite
// is an error, as are a bad or repeated name and more than MaxWindows
// windows. Errors name the window by its place, never its value. A reset
// time further than MaxResetAhead from now is dropped.
func cleanWindows(ws []Window, now time.Time) ([]api.UsageWindow, error) {
	if len(ws) > MaxWindows {
		return nil, fmt.Errorf("a script may report at most %d windows", MaxWindows)
	}
	out := make([]api.UsageWindow, 0, len(ws))
	seen := map[string]bool{}
	for i, w := range ws {
		if !windowName.MatchString(w.Name) {
			return nil, fmt.Errorf("window %d: the name must be 1 to 40 lowercase letters, digits, - or _, starting with a letter or digit", i+1)
		}
		if seen[w.Name] {
			return nil, fmt.Errorf("window %d: the name is used twice", i+1)
		}
		seen[w.Name] = true
		used := w.UsedPercent
		if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
			return nil, fmt.Errorf("window %d: the used percentage is not a number from 0 to 100", i+1)
		}
		cw := api.UsageWindow{Name: w.Name, UsedPercent: min(used, 100)}
		if !w.ResetsAt.IsZero() && !w.ResetsAt.After(now.Add(MaxResetAhead)) {
			at := w.ResetsAt.UTC()
			cw.ResetsAt = &at
		}
		out = append(out, cw)
	}
	return out, nil
}

// AccountKey names an account: 16 hex digits of the SHA-256 of the script's
// name, the cleaned account folder and the profile's environment sorted by
// name. Profiles that share all three share one reading.
func AccountKey(script string, p api.Profile) string {
	dir := p.AccountDir
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	env := make([][2]string, 0, len(p.Env))
	for k, v := range p.Env {
		env = append(env, [2]string{k, v})
	}
	slices.SortFunc(env, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	raw, _ := json.Marshal([]any{script, p.AccountDir != "", dir, env})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// cleanError makes a run's error text safe to show and keep: hidden
// characters escaped as \u{XXXX}, cut to 300 characters. The runner has
// already replaced secrets.
func cleanError(s string) string {
	if s == "" {
		return "the script failed"
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= maxErrorRunes {
			break
		}
		if r == utf8.RuneError || textsafe.IsHidden(r) {
			fmt.Fprintf(&b, `\u{%X}`, r)
		} else {
			b.WriteRune(r)
		}
		n++
	}
	return b.String()
}

// errText is the text kept for a failed run.
func errText(err error, deadline bool) string {
	if deadline {
		return "the script did not finish within 70 seconds"
	}
	return cleanError(err.Error())
}
