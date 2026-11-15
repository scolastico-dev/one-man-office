package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// pinsFile is the file below the run folder that keeps the pins across
// restarts of omo.
const pinsFile = "engine-pins.json"

// pinStore remembers which commit of each project was handed in for each
// review round of a ticket, so that exactly what was reviewed is merged,
// the squash commit omo made of a story in each project, and the commits a
// branch had moved on to when its merge failed for that. It is kept in
// the run folder (Options.Layout.Run), written on every change; without a
// run folder it lives in memory only.
type pinStore struct {
	mu     sync.Mutex
	path   string
	loaded bool
	data   pinData
}

// pinData is the file's content.
type pinData struct {
	// Reviewed maps "ticket/round" to the commit of each project.
	Reviewed map[string]map[string]string `json:"reviewed"`
	// Squashed maps "ticket/project" to the squash commit omo built.
	Squashed map[string]string `json:"squashed"`
	// Moved maps "ticket/round/project" to the commit a branch had moved on
	// to when its merge failed for that reason: a retry while the branch is
	// still there merges the reviewed commit and leaves the rest.
	Moved map[string]string `json:"moved,omitempty"`
	// Pushed maps "ticket/project" to what omo pushed for the ticket's
	// current export of the project: the export job's prompt and the check
	// of the pull request address it reports go by it, not by the
	// project's remote as it is configured at that time.
	Pushed map[string]pushRecord `json:"pushed,omitempty"`
}

// pushRecord is one push of an export.
type pushRecord struct {
	Commit string `json:"commit"`
	Branch string `json:"branch"`
	// Remote is the remote's name, Address its address without
	// credentials (gitops.Remote.Address).
	Remote  string `json:"remote"`
	Address string `json:"address"`
}

func reviewKey(ticket int64, round int) string { return fmt.Sprintf("%d/%d", ticket, round) }

func squashKey(ticket int64, project string) string { return fmt.Sprintf("%d/%s", ticket, project) }

func movedKey(ticket int64, round int, project string) string {
	return fmt.Sprintf("%d/%d/%s", ticket, round, project)
}

// ticketOfKey returns the ticket number a key starts with.
func ticketOfKey(key string) int64 {
	n, _, _ := strings.Cut(key, "/")
	id, _ := strconv.ParseInt(n, 10, 64)
	return id
}

// load reads the file once. The caller holds mu.
func (s *pinStore) load() error {
	if s.loaded {
		return nil
	}
	s.data = pinData{Reviewed: map[string]map[string]string{}, Squashed: map[string]string{}, Moved: map[string]string{},
		Pushed: map[string]pushRecord{}}
	if s.path != "" {
		raw, err := homefile.Read(s.path, homefile.MaxPinsFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			if err := json.Unmarshal(raw, &s.data); err != nil {
				return fmt.Errorf("read %s: %w", s.path, err)
			}
			if s.data.Reviewed == nil {
				s.data.Reviewed = map[string]map[string]string{}
			}
			if s.data.Squashed == nil {
				s.data.Squashed = map[string]string{}
			}
			if s.data.Moved == nil {
				s.data.Moved = map[string]string{}
			}
			if s.data.Pushed == nil {
				s.data.Pushed = map[string]pushRecord{}
			}
		}
	}
	s.loaded = true
	return nil
}

// save writes the file atomically. The caller holds mu.
func (s *pinStore) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// reviewed returns the commits handed in for round of ticket, by project;
// nil when none are recorded.
func (s *pinStore) reviewed(ticket int64, round int) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	return s.data.Reviewed[reviewKey(ticket, round)], nil
}

// setReviewed records the commits handed in for round of ticket.
func (s *pinStore) setReviewed(ticket int64, round int, commits map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	s.data.Reviewed[reviewKey(ticket, round)] = commits
	return s.save()
}

// squashed returns the squash commit omo built of story ticket in project,
// or "".
func (s *pinStore) squashed(ticket int64, project string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return "", err
	}
	return s.data.Squashed[squashKey(ticket, project)], nil
}

// setSquashed records the squash commit of story ticket in project.
func (s *pinStore) setSquashed(ticket int64, project, commit string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	s.data.Squashed[squashKey(ticket, project)] = commit
	return s.save()
}

// moved returns the commit the branch of ticket in project had moved on to
// when its merge for round failed for that reason, or "".
func (s *pinStore) moved(ticket int64, round int, project string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return "", err
	}
	return s.data.Moved[movedKey(ticket, round, project)], nil
}

// setMoved records that the merge for round of ticket failed because its
// branch in project had moved on to head.
func (s *pinStore) setMoved(ticket int64, round int, project, head string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	s.data.Moved[movedKey(ticket, round, project)] = head
	return s.save()
}

// pushed returns what omo pushed for the current export of project of
// ticket, and whether it pushed anything.
func (s *pinStore) pushed(ticket int64, project string) (pushRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return pushRecord{}, false, err
	}
	r, ok := s.data.Pushed[squashKey(ticket, project)]
	return r, ok, nil
}

// setPushed records what omo pushed for the export of project of ticket.
func (s *pinStore) setPushed(ticket int64, project string, r pushRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	s.data.Pushed[squashKey(ticket, project)] = r
	return s.save()
}

// prune forgets what no ticket of snap needs any more: the pins of rounds
// other than a ticket's current one (or, while it is in progress, the
// next, which a hand-in records before the board moves it), the pushes of
// tickets no longer exporting, and everything of tickets the snapshot no
// longer holds.
func (s *pinStore) prune(snap board.EngineSnapshot) error {
	byID := make(map[int64]board.EngineTicket, len(snap.Tickets))
	for _, t := range snap.Tickets {
		byID[t.ID] = t
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	changed := false
	for key := range s.data.Reviewed {
		t, ok := byID[ticketOfKey(key)]
		keep := ok && (key == reviewKey(t.ID, t.ReviewRound) ||
			(t.Column == api.ColumnInProgress && key == reviewKey(t.ID, t.ReviewRound+1)))
		if !keep {
			delete(s.data.Reviewed, key)
			changed = true
		}
	}
	for key := range s.data.Moved {
		t, ok := byID[ticketOfKey(key)]
		if !ok || !strings.HasPrefix(key, reviewKey(t.ID, t.ReviewRound)+"/") || t.Column != api.ColumnReview {
			delete(s.data.Moved, key)
			changed = true
		}
	}
	for key := range s.data.Pushed {
		t, ok := byID[ticketOfKey(key)]
		if !ok || t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting {
			delete(s.data.Pushed, key)
			changed = true
		}
	}
	for key := range s.data.Squashed {
		if _, ok := byID[ticketOfKey(key)]; !ok {
			delete(s.data.Squashed, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}
