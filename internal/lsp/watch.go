package lsp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

type fileWatcher struct {
	GlobPattern json.RawMessage `json:"globPattern"`
	Kind        int             `json:"kind"`
}

// LSP watcher globs have **, brace alternatives and ! character classes.
// Configured file selectors deliberately retain Go path.Match semantics.
func glob(pattern, name string) bool {
	if len(pattern) > 8192 {
		return false
	}
	var b strings.Builder
	b.WriteString("^")
	depth := 0
	for i := 0; i < len(pattern); i++ {
		switch ch := pattern[i]; ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			depth++
			b.WriteString("(?:")
		case '}':
			if depth == 0 {
				return false
			}
			depth--
			b.WriteByte(')')
		case ',':
			if depth > 0 {
				b.WriteByte('|')
			} else {
				b.WriteByte(',')
			}
		case '[':
			end := i + 1
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end == len(pattern) {
				return false
			}
			class := pattern[i+1 : end]
			b.WriteByte('[')
			if strings.HasPrefix(class, "!") {
				b.WriteByte('^')
				class = class[1:]
			} else if strings.HasPrefix(class, "^") {
				b.WriteByte('\\')
			}
			b.WriteString(class)
			b.WriteByte(']')
			i = end
		case '\\':
			if i+1 < len(pattern) {
				i++
				b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
			} else {
				return false
			}
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	if depth != 0 {
		return false
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(name)
}
func (s *languageServer) watches(watchers map[string][]fileWatcher, file string, kind int) bool {
	for _, ws := range watchers {
		for _, w := range ws {
			if w.Kind != 0 && w.Kind&kind == 0 {
				continue
			}
			var pattern string
			candidate := file
			if json.Unmarshal(w.GlobPattern, &pattern) != nil {
				var relative struct {
					BaseURI json.RawMessage `json:"baseUri"`
					Pattern string          `json:"pattern"`
				}
				if json.Unmarshal(w.GlobPattern, &relative) != nil {
					continue
				}
				var base string
				if json.Unmarshal(relative.BaseURI, &base) != nil {
					var folder struct {
						URI string `json:"uri"`
					}
					_ = json.Unmarshal(relative.BaseURI, &folder)
					base = folder.URI
				}
				if base != fileURI(s.runtime.root) {
					rel, ok := relativeFile(s.runtime.root, base)
					if !ok || !strings.HasPrefix(file, rel+"/") {
						continue
					}
					candidate = strings.TrimPrefix(file, rel+"/")
				}
				pattern = relative.Pattern
			} else if filepath.IsAbs(filepath.FromSlash(pattern)) {
				candidate = filepath.ToSlash(filepath.Join(s.runtime.root, filepath.FromSlash(file)))
			}
			if glob(pattern, candidate) {
				return true
			}
		}
	}
	return false
}

// diskError is retryable; it must not classify a healthy stdio server as dead.
type diskError struct{ error }
type cancelReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cancelReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
func (s *languageServer) watchSnapshot(watchers map[string][]fileWatcher) (map[string]string, error) {
	next := map[string]string{}
	err := filepath.WalkDir(s.runtime.root, func(name string, d fs.DirEntry, err error) error {
		if err := s.runtime.ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		file, err := filepath.Rel(s.runtime.root, name)
		if err != nil {
			return err
		}
		file = filepath.ToSlash(file)
		if !s.watches(watchers, file, 7) {
			return nil
		}
		f, err := homefile.Open(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, cancelReader{s.runtime.ctx, f})
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		next[file] = string(hash.Sum(nil))
		return nil
	})
	if err != nil {
		return nil, &diskError{fmt.Errorf("scan watched files: %w", err)}
	}
	return next, nil
}
func (s *languageServer) syncWatched() (bool, error) {
	s.mu.Lock()
	watchers := make(map[string][]fileWatcher, len(s.watchers))
	for id, ws := range s.watchers {
		watchers[id] = append([]fileWatcher{}, ws...)
	}
	baselines := s.watchBaselines
	s.watchBaselines = map[string]map[string]string{}
	s.mu.Unlock()
	next, err := s.watchSnapshot(watchers)
	if err != nil {
		return false, err
	}
	previous := make(map[string]string, len(s.watched))
	for file, hash := range s.watched {
		previous[file] = hash
	}
	for id, base := range baselines {
		if _, ok := watchers[id]; ok {
			for file, hash := range base {
				if _, exists := previous[file]; !exists {
					previous[file] = hash
				}
			}
		}
	}

	changes := []any{}
	add := func(file string, event, mask int) {
		if s.watches(watchers, file, mask) {
			changes = append(changes, map[string]any{"uri": fileURI(filepath.Join(s.runtime.root, filepath.FromSlash(file))), "type": event})
		}
	}
	for file, text := range next {
		old, exists := previous[file]
		if !exists {
			if s.watches(watchers, file, 7) {
				add(file, 1, 1)
			}
		} else if old != text {
			add(file, 2, 2)
		}
	}
	for file := range previous {
		if _, ok := next[file]; !ok {
			add(file, 3, 4)
		}
	}
	s.watched = next

	if len(changes) > 0 {
		s.mu.Lock()
		s.changed = true
		for file := range s.initial {
			if s.seen[file] {
				s.initial[file] = false
			}
		}
		s.mu.Unlock()
		return true, s.peer.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": changes})
	}
	return false, nil
}
