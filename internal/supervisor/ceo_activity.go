package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const ceoActivitySampleInterval = time.Second

type logSignature struct {
	files  int
	bytes  int64
	newest int64
}

// CEOActivityLoop estimates CEO active/idle time from transcript changes.
// The CEO is deliberately forbidden from entering the normal waiting state,
// so its DB lifecycle state cannot distinguish thinking/output from idleness.
func (s *Supervisor) CEOActivityLoop(ctx context.Context) {
	ticker := time.NewTicker(ceoActivitySampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.sampleCEOActivity(now)
		}
	}
}

func (s *Supervisor) sampleCEOActivity(now time.Time) {
	name := s.CEOName()
	if name == "" {
		s.mu.Lock()
		s.ceoActivityName = ""
		s.ceoActivityLast = time.Time{}
		s.ceoActivityLog = logSignature{}
		s.mu.Unlock()
		return
	}
	sig := s.ceoLogSignature(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ceoActivityName != name || s.ceoActivityLast.IsZero() {
		s.ceoActivityName = name
		s.ceoActivityLast = now
		s.ceoActivityLog = sig
		return
	}
	if now.Before(s.ceoActivityLast) {
		return
	}
	elapsed := now.Sub(s.ceoActivityLast)
	if sig != s.ceoActivityLog {
		s.ceoActivityActive += elapsed
	} else {
		s.ceoActivityIdle += elapsed
	}
	s.ceoActivityLast = now
	s.ceoActivityLog = sig
}

func (s *Supervisor) ceoLogSignature(name string) logSignature {
	entries, err := os.ReadDir(filepath.Join(s.OfficeDir, ".omo", "logs"))
	if err != nil {
		return logSignature{}
	}
	needle := "-" + name + ".log"
	var sig logSignature
	for _, entry := range entries {
		if entry.IsDir() || !strings.Contains(entry.Name(), needle) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		sig.files++
		sig.bytes += info.Size()
		if modified := info.ModTime().UnixNano(); modified > sig.newest {
			sig.newest = modified
		}
	}
	return sig
}
