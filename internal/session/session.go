// Package session runs one agent CLI in a PTY owned by omo, mirrors its
// output into a vt10x virtual screen (so a "current screen" always exists)
// and records a readable, deduplicated screen transcript.
package session

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hinshun/vt10x"
)

type Options struct {
	Cmd     string
	Args    []string
	Env     []string // appended to os.Environ()
	Dir     string
	LogPath string
	Rows    uint16
	Cols    uint16

	// LogMaxSizeKB caps the live transcript before it rotates (0 = never).
	// LogKeep bounds live rotation segments; -1 retains all segments. Office
	// inactive-session retention is applied separately by the supervisor.
	LogMaxSizeKB int
	LogKeep      int
}

type Session struct {
	opts     Options
	proc     terminalProcess
	term     vt10x.Terminal
	log      *rotatingWriter
	logOut   io.Writer // readable screen-delta transcript
	logLines []string
	logSeen  map[string]time.Time
	lastLog  time.Time
	logMu    sync.Mutex // guards transcript delta state and on-demand snapshots
	mu       sync.Mutex // guards term, subs and exitErr
	subs     map[chan struct{}]struct{}
	done     chan struct{}
	exitErr  error

	// writeInput overrides PTY writes for query replies (tests only).
	writeInput func([]byte)
}

func Start(o Options) (*Session, error) {
	if o.Rows == 0 {
		o.Rows = 40
	}
	if o.Cols == 0 {
		o.Cols = 120
	}
	logf, err := newRotatingWriter(o.LogPath, o.LogMaxSizeKB, o.LogKeep)
	if err != nil {
		return nil, err
	}
	proc, err := startProcess(o)
	if err != nil {
		logf.Close()
		return nil, err
	}
	s := &Session{
		opts:    o,
		proc:    proc,
		term:    vt10x.New(vt10x.WithSize(int(o.Cols), int(o.Rows))),
		log:     logf,
		logOut:  logf,
		logSeen: map[string]time.Time{},
		subs:    map[chan struct{}]struct{}{},
		done:    make(chan struct{}),
	}
	go s.pump()
	return s, nil
}

func (s *Session) pump() {
	buf := make([]byte, 4096)
	var carry []byte // trailing bytes of a query split across two reads
	for {
		n, err := s.proc.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.term.Write(buf[:n])
			s.mu.Unlock()
			if s.logSnapshotDue() {
				s.writeLogSnapshot()
			}
			carry = s.answerQueries(append(carry, buf[:n]...))
			s.notify()
		}
		if err != nil {
			break
		}
	}
	s.writeLogSnapshot()
	err := s.proc.Wait()
	s.mu.Lock()
	s.exitErr = err
	s.mu.Unlock()
	s.log.Close()
	s.proc.Close()
	close(s.done)
	s.notify()
}

func (s *Session) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default: // coalesce: subscriber already has a pending notification
		}
	}
}

// Subscribe returns a channel that receives (coalesced) screen-change
// notifications and a cancel func that unsubscribes.
func (s *Session) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

func (s *Session) Screen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term.String()
}

func (s *Session) SendText(text string) error {
	_, err := s.proc.Write([]byte(text))
	return err
}

func (s *Session) SendLine(text string) error { return s.SendText(text + "\r") }

// SubmitDelay separates typed text from its Enter. Full-screen CLIs (Claude
// Code among them) treat a fast burst of bytes as a *paste*, in which a
// trailing CR inserts a literal newline instead of submitting. Sending the
// Enter on its own, a moment later, is what actually submits the line.
var SubmitDelay = 300 * time.Millisecond

// SendPrompt types text into the session and submits it.
func (s *Session) SendPrompt(text string) error {
	if err := s.SendText(text); err != nil {
		return err
	}
	time.Sleep(SubmitDelay)
	return s.SendText("\r")
}

func (s *Session) Resize(rows, cols uint16) error {
	if err := s.proc.Resize(rows, cols); err != nil {
		return err
	}
	s.mu.Lock()
	s.term.Resize(int(cols), int(rows))
	s.mu.Unlock()
	return nil
}

func (s *Session) Kill() error {
	return s.proc.Kill()
}

// logDedupWindow suppresses lines a full-screen program recently moved or
// redrew. A bounded window still permits the same meaningful output to appear
// again later in a long-running session.
const logDedupWindow = time.Minute

// writeLogSnapshot writes only meaningful screen lines that appeared since
// the previous snapshots. Full-screen CLIs redraw and move the same content
// constantly, while spinners and footer chrome change every few milliseconds;
// neither belongs dozens of times in a readable transcript.
func (s *Session) writeLogSnapshot() {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.mu.Lock()
	screen := s.term.String()
	s.mu.Unlock()
	now := time.Now()
	changed := s.screenLogDelta(screen, now)
	if len(changed) > 0 {
		_, _ = s.logOut.Write([]byte(strings.Join(changed, "\n") + "\n"))
	}
	s.lastLog = now
}

func (s *Session) logSnapshotDue() bool {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	return time.Since(s.lastLog) >= 250*time.Millisecond
}

func (s *Session) screenLogDelta(screen string, now time.Time) []string {
	lines := strings.Split(screen, "\n")
	previous := make(map[string]struct{}, len(s.logLines))
	for _, line := range s.logLines {
		line = strings.TrimRight(line, " \t\r")
		if line != "" {
			previous[line] = struct{}{}
		}
	}
	if s.logSeen == nil {
		s.logSeen = make(map[string]time.Time)
	}
	if len(s.logSeen) > 4096 {
		cutoff := now.Add(-logDedupWindow)
		for key, seen := range s.logSeen {
			if seen.Before(cutoff) {
				delete(s.logSeen, key)
			}
		}
	}
	emitted := make(map[string]struct{})
	var changed []string
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || isLogChrome(line) {
			continue
		}
		// Movement to another row is not new transcript content.
		if _, ok := previous[line]; ok {
			continue
		}
		key := normalizedLogKey(line)
		if _, ok := emitted[key]; ok {
			continue
		}
		if seen, ok := s.logSeen[key]; ok && now.Sub(seen) < logDedupWindow {
			continue
		}
		changed = append(changed, line)
		emitted[key] = struct{}{}
		s.logSeen[key] = now
	}
	s.logLines = lines
	return changed
}

func isLogChrome(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || line == "❯" || strings.HasPrefix(line, "⏵⏵ ") || strings.Contains(line, "· /effort") {
		return true
	}
	separator := true
	for _, r := range line {
		if !strings.ContainsRune("─━═╌╍┄┅┈┉-", r) {
			separator = false
			break
		}
	}
	if separator {
		return true
	}
	first, rest, _ := strings.Cut(line, " ")
	return strings.ContainsRune("✢✶✽✻✳✦·*", []rune(first)[0]) && strings.Contains(rest, "…")
}

func normalizedLogKey(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "● ")
	if strings.HasPrefix(line, "Running ") {
		if end := strings.IndexRune(line, '…'); end >= 0 {
			return line[:end+len("…")]
		}
	}
	return line
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) ExitErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitErr
}

// TailLog returns the last n lines of the readable session transcript.
func (s *Session) TailLog(n int) ([]string, error) {
	// A quiet full-screen process may not produce another PTY read after its
	// latest redraw. Capture its current screen before serving a live tail.
	s.writeLogSnapshot()
	raw, err := os.ReadFile(s.opts.LogPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
