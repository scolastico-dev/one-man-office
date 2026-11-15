package usage

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"regexp"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// maxStateFile bounds the kept readings (<run>/usage.json).
const maxStateFile = homefile.MaxSettingsFile

// keptIgnored starts the one log line written when the kept readings cannot
// be used; a fixed reason follows, never the file's content.
const keptIgnored = "usage: the kept usage readings were ignored: "

var accountKeyPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// keptFile is the content of <run>/usage.json: the windows of each account's
// last successful run. It never holds an error text, a log line or anything
// a script read.
type keptFile struct {
	Accounts []keptAccount `json:"accounts"`
}

type keptAccount struct {
	Key       string            `json:"key"`
	Script    string            `json:"script"`
	Windows   []api.UsageWindow `json:"windows"`
	FetchedAt time.Time         `json:"fetched_at"`
}

// loadKept reads the kept readings by account key. A missing file gives none
// and no message. A file that is not a regular file, is too large or is not
// valid gives none and a fixed reason. An entry is dropped when its key is
// malformed, its reading is older than MaxReadingAge or its windows are not
// valid; a window whose reset time has passed is dropped from its entry.
func loadKept(path string, now time.Time) (map[string]keptAccount, string) {
	out := map[string]keptAccount{}
	if path == "" {
		return out, ""
	}
	raw, err := homefile.Read(path, maxStateFile)
	if errors.Is(err, os.ErrNotExist) {
		return out, ""
	}
	if err != nil {
		if why, ok := homefile.Refusal(err, maxStateFile); ok {
			return out, why
		}
		return out, "the file could not be read"
	}
	var f keptFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return out, "the file is not valid"
	}
	for _, a := range f.Accounts {
		if !accountKeyPattern.MatchString(a.Key) || a.FetchedAt.IsZero() || now.Sub(a.FetchedAt) > MaxReadingAge || len(a.Windows) > MaxWindows {
			continue
		}
		if _, dup := out[a.Key]; dup {
			continue
		}
		ws, ok := keptWindows(a.Windows, now)
		if !ok {
			continue
		}
		a.Windows = ws
		a.FetchedAt = a.FetchedAt.UTC()
		out[a.Key] = a
	}
	return out, ""
}

// keptWindows checks the windows of a kept entry as a script's would be and
// drops those whose reset time has passed.
func keptWindows(ws []api.UsageWindow, now time.Time) ([]api.UsageWindow, bool) {
	out := make([]api.UsageWindow, 0, len(ws))
	seen := map[string]bool{}
	for _, w := range ws {
		if !windowName.MatchString(w.Name) || seen[w.Name] || math.IsNaN(w.UsedPercent) || w.UsedPercent < 0 || w.UsedPercent > 100 {
			return nil, false
		}
		seen[w.Name] = true
		if w.ResetsAt != nil {
			if !w.ResetsAt.After(now) {
				continue
			}
			if w.ResetsAt.After(now.Add(MaxResetAhead)) {
				w.ResetsAt = nil
			}
		}
		out = append(out, w)
	}
	return out, true
}

// saveKept writes the kept readings atomically with mode 0600.
func saveKept(path string, accounts []keptAccount) error {
	if accounts == nil {
		accounts = []keptAccount{}
	}
	raw, err := json.Marshal(keptFile{Accounts: accounts})
	if err != nil {
		return err
	}
	return globalhome.WriteFileAtomic(path, raw, 0o600)
}
