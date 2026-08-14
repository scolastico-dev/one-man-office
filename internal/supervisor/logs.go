package supervisor

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type logGroup struct {
	base   string
	files  []string
	newest time.Time
}

// PruneInactiveLogs keeps the newest configured number of completed session
// log groups. Every live agent is exempt, so keep: 10 may legitimately leave
// more than ten groups on disk. keep: -1 disables pruning.
func (s *Supervisor) PruneInactiveLogs() {
	keep := s.Config().Logs.Keep
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
