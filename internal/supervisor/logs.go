package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

type logGroup struct {
	base   string
	files  []string
	newest time.Time
}

func (s *Supervisor) registerLogVerbs(srv *sockd.Server) {
	srv.Handle("agent.logs", func(agentID string, args json.RawMessage) (any, error) {
		if agentID != "user" {
			caller, err := db.GetAgent(s.DB, agentID)
			if err != nil {
				return nil, err
			}
			if caller.Role != "ceo" && caller.Role != "firefighter" {
				return nil, fmt.Errorf("only the user, CEO, or firefighter may read developer logs")
			}
		}
		var a proto.LogTailArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if a.Lines < 1 || a.Lines > 10000 {
			return nil, fmt.Errorf("lines must be between 1 and 10000")
		}
		target, err := db.GetAgent(s.DB, a.Name)
		if err != nil || target.Role != "developer" {
			return nil, fmt.Errorf("no active developer named %q", a.Name)
		}
		sess, ok := s.Session(target.Name)
		if !ok || target.State == "done" || target.State == "dead" {
			return nil, fmt.Errorf("no active developer named %q", a.Name)
		}
		lines, err := sess.TailLog(a.Lines)
		if err != nil {
			return nil, fmt.Errorf("read log for %s: %w", target.Name, err)
		}
		return proto.LogTailResponse{Lines: lines}, nil
	})
}

// PruneInactiveLogs keeps the newest configured number of completed session
// log groups. Every live agent is exempt, so keep: 10 may legitimately leave
// more than ten groups on disk. keep: -1 disables pruning.
func (s *Supervisor) PruneInactiveLogs() {
	keep := s.Cfg.Logs.Keep
	if keep < 0 {
		return
	}
	dir := filepath.Join(s.OfficeDir, ".omo", "logs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	s.mu.Lock()
	active := make([]string, 0, len(s.sessions))
	for name := range s.sessions {
		active = append(active, name)
	}
	s.mu.Unlock()
	groups := map[string]*logGroup{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		idx := strings.Index(name, ".log")
		if idx < 0 {
			continue
		}
		base := name[:idx+4]
		g := groups[base]
		if g == nil {
			g = &logGroup{base: base}
			groups[base] = g
		}
		path := filepath.Join(dir, name)
		g.files = append(g.files, path)
		if fi, err := entry.Info(); err == nil && fi.ModTime().After(g.newest) {
			g.newest = fi.ModTime()
		}
	}
	var inactive []*logGroup
	for _, g := range groups {
		isActive := false
		for _, name := range active {
			if strings.HasSuffix(g.base, "-"+name+".log") {
				isActive = true
				break
			}
		}
		if !isActive {
			inactive = append(inactive, g)
		}
	}
	sort.Slice(inactive, func(i, j int) bool { return inactive[i].newest.After(inactive[j].newest) })
	if keep >= len(inactive) {
		return
	}
	for _, g := range inactive[keep:] {
		for _, path := range g.files {
			_ = os.Remove(path)
		}
	}
}
