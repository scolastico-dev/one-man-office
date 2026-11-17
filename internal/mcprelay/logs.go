package mcprelay

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/secreturl"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Bounds of a server's log file: rotated at logMaxBytes, keeping logKeep
// older segments; a single line is cut at logMaxLine bytes.
const (
	logMaxBytes = 10 << 20
	logKeep     = 3
	logMaxLine  = 4096
)

// redacted replaces a secret value in log lines, errors and states.
const redacted = "[redacted]"

// minSecret is the shortest value treated as a secret: shorter values are
// not secrets worth hiding, and replacing them would garble every line.
const minSecret = 8

// redactor hides the secret values of a server: its env and header values,
// as written and as expanded, the output of its header command, its OAuth
// client secret and tokens, and the secrets of its address, which is shown
// as secreturl.Display renders it.
type redactor struct {
	mu      sync.RWMutex
	secrets []string
	// addresses are the forms of an address with secrets, each replaced
	// by its display form before the secrets are.
	addresses []addressForm
}

type addressForm struct{ form, display string }

// addAddress learns the address raw: its forms are shown as Display
// renders it, and its secret parts are hidden wherever they appear.
func (r *redactor) addAddress(raw string) {
	display := secreturl.Display(raw)
	r.mu.Lock()
	for _, f := range secreturl.Forms(raw) {
		if !slices.Contains(r.addresses, addressForm{f, display}) {
			r.addresses = append(r.addresses, addressForm{f, display})
		}
	}
	slices.SortFunc(r.addresses, func(a, b addressForm) int { return len(b.form) - len(a.form) })
	r.mu.Unlock()
	r.add(secreturl.Values(raw)...)
}

func (r *redactor) add(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < minSecret || slices.Contains(r.secrets, v) {
			continue
		}
		r.secrets = append(r.secrets, v)
	}
	// Longer values first, so a secret that contains another is hidden
	// whole.
	slices.SortFunc(r.secrets, func(a, b string) int { return len(b) - len(a) })
}

func (r *redactor) redact(s string) string {
	if r == nil {
		return s
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.addresses {
		s = strings.ReplaceAll(s, a.form, a.display)
	}
	for _, v := range r.secrets {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, redacted)
		}
	}
	return s
}

// serverLog is the log file of one server: lifecycle lines and the
// standard error of its processes, redacted and cleaned of characters
// that could steer a terminal.
type serverLog struct {
	path   string
	redact *redactor
	mu     sync.Mutex
	f      *os.File
	size   int64
}

func newServerLog(dir, name string, r *redactor) *serverLog {
	if dir == "" {
		return &serverLog{redact: r}
	}
	return &serverLog{path: filepath.Join(dir, "mcp-"+name+".log"), redact: r}
}

// printf writes one line.
func (l *serverLog) printf(format string, args ...any) {
	l.line(fmt.Sprintf(format, args...))
}

// line writes msg as one line: redacted, hidden characters replaced, cut
// at logMaxLine.
func (l *serverLog) line(msg string) {
	if l == nil || l.path == "" {
		return
	}
	msg = cleanLine(l.redact.redact(msg))
	entry := time.Now().UTC().Format(time.RFC3339Nano) + " " + msg + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
			return
		}
		f, err := homefile.Append(l.path, 0o600)
		if err != nil {
			return
		}
		l.f = f
		if fi, err := f.Stat(); err == nil {
			l.size = fi.Size()
		}
	}
	if l.size+int64(len(entry)) > logMaxBytes {
		l.rotate()
		if l.f == nil {
			return
		}
	}
	n, _ := l.f.WriteString(entry)
	l.size += int64(n)
}

// rotate must be called with the lock held.
func (l *serverLog) rotate() {
	l.f.Close()
	l.f = nil
	os.Remove(fmt.Sprintf("%s.%d", l.path, logKeep))
	for i := logKeep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
	}
	os.Rename(l.path, l.path+".1")
	f, err := homefile.Append(l.path, 0o600)
	if err != nil {
		return
	}
	l.f = f
	l.size = 0
}

func (l *serverLog) close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}

// copyLines writes each line read from r into the log with prefix, until r
// ends. A line longer than logMaxLine is cut.
func (l *serverLog) copyLines(r io.Reader, prefix string) {
	br := bufio.NewReaderSize(r, logMaxLine)
	for {
		line, isPrefix, err := br.ReadLine()
		if len(line) > 0 || (err == nil && !isPrefix) {
			l.line(prefix + string(line))
		}
		for isPrefix && err == nil {
			// Drop the rest of an overlong line.
			_, isPrefix, err = br.ReadLine()
		}
		if err != nil {
			return
		}
	}
}

// cleanLine replaces hidden characters and cuts s at logMaxLine bytes.
func cleanLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if textsafe.IsHidden(r) || r == utf8.RuneError {
			return '?'
		}
		return r
	}, s)
	if len(s) > logMaxLine {
		s = s[:logMaxLine]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s
}

// readLog returns the last max lines of the log at path, older segments
// included when the current one is shorter.
func readLog(path string, max int) ([]api.MCPLogLine, error) {
	var lines []api.MCPLogLine
	for i := 0; i <= logKeep && len(lines) < max; i++ {
		p := path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", path, i)
		}
		data, err := homefile.Read(p, logMaxBytes+logMaxLine)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return nil, err
		}
		var seg []api.MCPLogLine
		for raw := range bytes.SplitSeq(data, []byte("\n")) {
			if len(raw) == 0 {
				continue
			}
			at, msg, _ := strings.Cut(string(raw), " ")
			t, err := time.Parse(time.RFC3339Nano, at)
			if err != nil {
				msg = string(raw)
			}
			seg = append(seg, api.MCPLogLine{At: t, Message: msg})
		}
		lines = append(seg, lines...)
	}
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	if lines == nil {
		lines = []api.MCPLogLine{}
	}
	return lines, nil
}
