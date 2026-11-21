package agents

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// completeWithin runs fn on its own goroutine and waits for it. The limit is
// not used: an operation that never completes ends with the test binary's own
// time limit and a dump of every goroutine. Behavioral deadlines remain
// configured on the operation itself.
func completeWithin(t *testing.T, _ time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	<-done
}

// fakeTerminal stands in for an agent PTY: it records what omo types.
type fakeTerminal struct {
	mu          sync.Mutex
	typed       []string
	prompts     []string
	done        chan struct{}
	exitErr     error
	failing     bool
	screen      string
	beforeReady func()
}

func newFakeTerminal() *fakeTerminal { return &fakeTerminal{done: make(chan struct{})} }

func (f *fakeTerminal) SendTextAndKeysIf(text, keys string, ready func() bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.beforeReady != nil {
		hook := f.beforeReady
		f.beforeReady = nil
		hook()
	}
	if !ready() {
		return false, nil
	}
	if f.failing {
		return true, errors.New("terminal closed")
	}
	f.typed = append(f.typed, text+keys)
	return true, nil
}

func TestNotifyDeferredFiltersAtTerminalDelivery(t *testing.T) {
	for _, boundary := range []string{"queued", "input-lock"} {
		t.Run(boundary, func(t *testing.T) {
			term := newFakeTerminal()
			n := newNotifier(term, &inputClock{}, time.Now, 0, 0)
			a := &ptyAgent{notes: n}
			ignored := false
			a.NotifyDeferred(func() string {
				if ignored {
					return ""
				}
				return "ignored issue"
			})
			a.NotifyDeferred(func() string {
				if ignored {
					return "remaining issue"
				}
				return "ignored issue and remaining issue"
			})
			a.Notify("ordinary notification")
			if boundary == "queued" {
				ignored = true
			} else {
				term.beforeReady = func() { ignored = true }
			}
			if wait := n.flush(); wait != 0 {
				t.Fatalf("flush waited %v", wait)
			}
			got, _ := term.snapshot()
			if len(got) != 2 || got[0] != "omo: remaining issue\r" || got[1] != "omo: ordinary notification\r" {
				t.Fatalf("terminal received %q", got)
			}
		})
	}
}

func (f *fakeTerminal) SendPartsAndKeys(parts []string, keys string) error {
	_, err := f.SendTextAndKeysIf(strings.Join(parts, ""), keys, func() bool { return true })
	return err
}

func (f *fakeTerminal) SendPrompt(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, text)
	return nil
}

func (f *fakeTerminal) SendPromptShown(text string, shown func() bool, _ time.Duration) (bool, error) {
	_ = f.SendPrompt(text)
	return shown(), nil
}

func (f *fakeTerminal) SendSubmit() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, "\r")
	return nil
}

func (f *fakeTerminal) Screen() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.screen
}

func (f *fakeTerminal) SendText(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.typed = append(f.typed, "user:"+text)
	return nil
}

func (f *fakeTerminal) Resize(uint16, uint16) error { return nil }
func (f *fakeTerminal) Done() <-chan struct{}       { return f.done }
func (f *fakeTerminal) ExitErr() error              { return f.exitErr }

func (f *fakeTerminal) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.typed...), append([]string(nil), f.prompts...)
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	for !cond() {
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNotifyDefersWhileUserTypes(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	term := newFakeTerminal()
	input := &inputClock{}
	adapter := &terminalAdapter{sess: term, out: newOutputPipe(), input: input, now: now, kill: func() error { return nil }}
	if _, err := adapter.Write([]byte("half a sentence")); err != nil {
		t.Fatal(err)
	}

	held := newNotifier(term, input, now, time.Hour, 2*time.Hour)
	held.enqueue("omo: x")
	if wait := held.flush(); wait != time.Hour {
		t.Fatalf("hold-back = %v, want 1h", wait)
	}
	if typed, _ := term.snapshot(); len(typed) != 1 {
		t.Fatalf("notification typed into the user's line: %q", typed)
	}

	// Once the hold-back has passed, every queued line goes out in order.
	held.enqueue("omo: y")
	clock = clock.Add(time.Hour)
	if wait := held.flush(); wait != 0 {
		t.Fatalf("flush after the hold-back returned %v", wait)
	}
	typed, _ := term.snapshot()
	if len(typed) != 3 || typed[1] != "omo: x\r" || typed[2] != "omo: y\r" {
		t.Fatalf("typed = %q", typed)
	}

	direct := newFakeTerminal()
	dInput := &inputClock{}
	dAdapter := &terminalAdapter{sess: direct, out: newOutputPipe(), input: dInput, now: now, kill: func() error { return nil }}
	if _, err := dAdapter.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	immediate := newNotifier(direct, dInput, now, 0, DefaultMaxHold)
	immediate.enqueue("omo: x")
	if wait := immediate.flush(); wait != 0 {
		t.Fatalf("no hold-back configured, flush waited %v", wait)
	}
	if got, _ := direct.snapshot(); len(got) != 2 || got[1] != "omo: x\r" {
		t.Fatalf("typed = %q", got)
	}
}

func TestNotifyIgnoresAutomaticTerminalInput(t *testing.T) {
	automatic := map[string]string{
		"focus in":          "\x1b[I",
		"focus out":         "\x1b[O",
		"focus in and out":  "\x1b[I\x1b[O\x1b[I",
		"X10 mouse":         "\x1b[M !!",
		"SGR mouse":         "\x1b[<0;12;5M\x1b[<0;12;5m",
		"urxvt mouse":       "\x1b[32;12;5M",
		"primary DA":        "\x1b[?1;2c",
		"secondary DA":      "\x1b[>0;276;0c",
		"cursor position":   "\x1b[24;80R",
		"status report":     "\x1b[0n",
		"mode report":       "\x1b[?2004;1$y",
		"window report":     "\x1b[8;30;100t",
		"OSC colour answer": "\x1b]11;rgb:0000/0000/0000\x1b\\",
		"OSC with BEL":      "\x1b]10;rgb:ffff/ffff/ffff\x07",
		"DCS answer":        "\x1bP>|xterm.js(5.5.0)\x1b\\",
		"paste markers":     "\x1b[200~\x1b[201~",
		"cut-off report":    "\x1b[<0;12",
		"nothing":           "",
	}
	for name, input := range automatic {
		if typedByPerson([]byte(input)) {
			t.Errorf("%s (%q) counted as typing", name, input)
		}
	}
	typed := map[string]string{
		"letter":             "a",
		"enter":              "\r",
		"escape key":         "\x1b",
		"cursor up":          "\x1b[A",
		"application cursor": "\x1bOA",
		"F5":                 "\x1b[15~",
		"alt+x":              "\x1bx",
		"pasted text":        "\x1b[200~hello\x1b[201~",
		"focus then key":     "\x1b[Ia",
		"key after mouse":    "\x1b[<0;1;1Mq",
	}
	for name, input := range typed {
		if !typedByPerson([]byte(input)) {
			t.Errorf("%s (%q) not counted as typing", name, input)
		}
	}

	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	term := newFakeTerminal()
	input := &inputClock{}
	adapter := &terminalAdapter{sess: term, out: newOutputPipe(), input: input, now: func() time.Time { return clock }, kill: func() error { return nil }}
	for _, report := range automatic {
		if _, err := adapter.Write([]byte(report)); err != nil {
			t.Fatal(err)
		}
	}
	if !input.lastInput().IsZero() {
		t.Fatal("automatic input held notifications back")
	}
	if typed, _ := term.snapshot(); len(typed) != len(automatic) {
		t.Fatalf("automatic input did not reach the agent: %q", typed)
	}
	if _, err := adapter.Write([]byte("k")); err != nil {
		t.Fatal(err)
	}
	if input.lastInput() != clock {
		t.Fatal("a key press did not count as typing")
	}
}

func TestNotifyHoldBackIsBounded(t *testing.T) {
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	term := newFakeTerminal()
	input := &inputClock{}
	n := newNotifier(term, input, now, 30*time.Second, 2*time.Minute)
	n.enqueue("still typing")
	// Somebody types every 20 seconds, never leaving 30 idle seconds.
	for elapsed := time.Duration(0); elapsed < 2*time.Minute; elapsed += 20 * time.Second {
		input.record(clock)
		if wait := n.flush(); wait <= 0 {
			t.Fatalf("delivered after %v of continuous typing", elapsed)
		}
		if typed, _ := term.snapshot(); len(typed) != 0 {
			t.Fatalf("typed early: %q", typed)
		}
		clock = clock.Add(20 * time.Second)
	}
	input.record(clock)
	if wait := n.flush(); wait != 0 {
		t.Fatalf("still held after the bound: %v", wait)
	}
	if typed, _ := term.snapshot(); len(typed) != 1 || typed[0] != "omo: still typing\r" {
		t.Fatalf("typed = %q", typed)
	}
	// The wait reported while held never exceeds what is left of the bound.
	n.enqueue("next")
	if wait := n.flush(); wait != 30*time.Second {
		t.Fatalf("fresh line waits %v, want the debounce", wait)
	}
	clock = clock.Add(110 * time.Second)
	input.record(clock)
	if wait := n.flush(); wait != 10*time.Second {
		t.Fatalf("wait near the bound = %v, want 10s", wait)
	}
}

func TestNotifyRunsInOrderAndStopsAtExit(t *testing.T) {
	term := newFakeTerminal()
	n := newNotifier(term, &inputClock{}, time.Now, time.Millisecond, DefaultMaxHold)
	n.input.record(time.Now())
	go n.run()
	for _, line := range []string{"omo: 1", "omo: 2", "omo: 3"} {
		n.enqueue(line)
	}
	waitUntil(t, 5*time.Second, func() bool { typed, _ := term.snapshot(); return len(typed) == 3 })
	typed, _ := term.snapshot()
	for i, want := range []string{"omo: 1\r", "omo: 2\r", "omo: 3\r"} {
		if typed[i] != want {
			t.Fatalf("typed = %q", typed)
		}
	}
	close(term.done)
	n.enqueue("omo: late")
	n.mu.Lock()
	queued := len(n.queue)
	n.mu.Unlock()
	if queued != 0 {
		t.Fatalf("%d lines queued after exit", queued)
	}
}

func TestNotificationIsOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"omo: a\nb":             "omo: a b",
		"  omo:\r\n\t x  ":      "omo: x",
		"omo: \x1b[2Jclear\x03": "omo: [2Jclear",
		"\n\n":                  "",
		"omo: a\u202eb\u200bc":  "omo: abc",
	} {
		if got := singleLine(in); got != want {
			t.Errorf("singleLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotificationLineIsSafe(t *testing.T) {
	for in, want := range map[string]string{
		"/clear":                            "omo: /clear",
		"!rm -rf /":                         "omo: !rm -rf /",
		"#memory note":                      "omo: #memory note",
		"omo: already prefixed":             "omo: already prefixed",
		"omo:/x":                            "omo: /x",
		"omo: ":                             "",
		"\n":                                "",
		"a\rb\nc":                           "omo: a b c",
		"\x1b[31mred\x1b[0m":                "omo: [31mred[0m",
		"\x1b[200~/paste\x1b[201~\r":        "omo: [200~/paste[201~",
		"one\u2028two\u2029three\u0085four": "omo: one two three four",
		"vt\vff\fend":                       "omo: vt ff end",
		"\u2028/slash after a separator":    "omo: /slash after a separator",
	} {
		if got := notificationLine(in); got != want {
			t.Errorf("notificationLine(%q) = %q, want %q", in, got, want)
		}
	}

	huge := notificationLine(strings.Repeat("ä", 100<<10))
	if n := utf8.RuneCountInString(huge); n != MaxNotifyLength {
		t.Fatalf("100 KB line became %d characters, want %d", n, MaxNotifyLength)
	}
	if !utf8.ValidString(huge) || !strings.HasSuffix(huge, "…") || !strings.HasPrefix(huge, NotifyPrefix) {
		t.Fatalf("cut line is malformed: %q...", huge[:20])
	}

	term := newFakeTerminal()
	n := newNotifier(term, &inputClock{}, time.Now, 0, DefaultMaxHold)
	n.enqueue("/compact\r\nnext")
	n.flush()
	if typed, _ := term.snapshot(); len(typed) != 1 || typed[0] != "omo: /compact next\r" {
		t.Fatalf("typed = %q", typed)
	}
}

// lateAgent is an agent CLI that acknowledges its start prompt late: it
// starts printing after the prompt numbered printsAfter (0: never) and
// reports ready only after ackAfter readiness checks.
type lateAgent struct {
	*fakeTerminal
	printsAfter int
	ackAfter    int

	mu       sync.Mutex
	checks   int
	printing bool
	printed  time.Time
}

func (l *lateAgent) SendPromptShown(text string, _ func() bool, _ time.Duration) (bool, error) {
	_ = l.fakeTerminal.SendPrompt(text)
	_, prompts := l.snapshot()
	if l.printsAfter > 0 && len(prompts) >= l.printsAfter {
		l.mu.Lock()
		l.printing = true
		l.mu.Unlock()
	}
	return false, nil
}

func (l *lateAgent) awaiting() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.checks++
	return l.ackAfter == 0 || l.checks <= l.ackAfter
}

// lastOutput is the output clock: a printing agent has shown output by
// the time anybody looks.
func (l *lateAgent) lastOutput() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.printing && l.printed.IsZero() {
		l.printed = time.Now()
	}
	return l.printed
}

func (l *lateAgent) sends() int {
	_, prompts := l.snapshot()
	return len(prompts)
}

func TestStartPromptDeliveryRules(t *testing.T) {
	quick := Delivery{Inject: true, Delay: 0, RetryWait: time.Millisecond, Retries: 3}
	noWarn := func(m string) { t.Error(m) }
	deliver := func(l *lateAgent, launched bool, d Delivery) {
		deliverStartPrompt(l, "run omo ai ready", launched, d, l.awaiting, l.lastOutput, time.Now, noWarn)
	}

	// A CLI that swallowed the typed prompt and shows nothing gets it again.
	silent := &lateAgent{fakeTerminal: newFakeTerminal()}
	deliver(silent, false, quick)
	if n := silent.sends(); n != 4 {
		t.Fatalf("silent agent: %d sends, want 1 + 3 retries", n)
	}

	// A CLI that works on the prompt but acknowledges only after several
	// retry intervals must not get it a second time.
	working := &lateAgent{fakeTerminal: newFakeTerminal(), printsAfter: 1, ackAfter: 3}
	deliver(working, false, quick)
	if n := working.sends(); n != 1 {
		t.Fatalf("late acknowledgement with output: %d sends, want 1", n)
	}

	// Output that starts only after a resend stops further resends.
	second := &lateAgent{fakeTerminal: newFakeTerminal(), printsAfter: 2}
	deliver(second, false, quick)
	if n := second.sends(); n != 2 {
		t.Fatalf("output after the second send: %d sends, want 2", n)
	}

	// An acknowledgement ends delivery.
	acked := &lateAgent{fakeTerminal: newFakeTerminal(), ackAfter: 1}
	deliver(acked, false, quick)
	if n := acked.sends(); n != 1 {
		t.Fatalf("acknowledged after the first send: %d sends", n)
	}

	// A prompt passed as a launch argument is never typed, however late
	// the agent acknowledges and whether or not it prints.
	for _, l := range []*lateAgent{{fakeTerminal: newFakeTerminal()}, {fakeTerminal: newFakeTerminal(), printsAfter: 1, ackAfter: 3}} {
		deliver(l, true, quick)
		if n := l.sends(); n != 0 {
			t.Fatalf("launch argument: %d sends, want 0", n)
		}
	}

	off := &lateAgent{fakeTerminal: newFakeTerminal()}
	deliver(off, false, Delivery{Retries: 3})
	if n := off.sends(); n != 0 {
		t.Fatalf("inject off: %d sends", n)
	}

	ended := &lateAgent{fakeTerminal: newFakeTerminal()}
	close(ended.done)
	deliver(ended, false, Delivery{Inject: true, Delay: time.Hour, Retries: 3, RetryWait: time.Hour})
	if n := ended.sends(); n != 0 {
		t.Fatalf("after exit: %d sends", n)
	}
}

func readAll(p *outputPipe) []byte {
	var out []byte
	buf := make([]byte, 32<<10)
	for {
		n, err := p.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out
		}
	}
}

func TestOutputPipeNeverBlocksTheWriter(t *testing.T) {
	p := newOutputPipe()
	chunk := make([]byte, 64<<10)
	for i := 0; i < 64; i++ { // 4 MiB with nobody reading
		p.write(chunk)
	}
	if p.size > outputBufferLimit || len(p.ring) != outputBufferLimit {
		t.Fatalf("buffer holds %d bytes in %d", p.size, len(p.ring))
	}
	p.end()
	if total := len(readAll(p)); total != outputBufferLimit {
		t.Fatalf("read %d bytes after end, want the last %d", total, outputBufferLimit)
	}
}

func TestOutputPipeKeepsOrderAcrossTheRing(t *testing.T) {
	p := newOutputPipe()
	var want []byte
	buf := make([]byte, 7000)
	// Writes and reads of odd sizes move the ring's start around its end.
	for i := 0; i < 2000; i++ {
		line := []byte(fmt.Sprintf("line %d %s\n", i, strings.Repeat("x", i%300)))
		p.write(line)
		want = append(want, line...)
		if i%3 == 0 {
			n, err := p.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf[:n], want[:n]) {
				t.Fatalf("read %q, want %q", buf[:n], want[:n])
			}
			want = want[n:]
		}
	}
	p.end()
	if got := readAll(p); !bytes.Equal(got, want) {
		t.Fatalf("drained %d bytes, want %d", len(got), len(want))
	}
}

func TestOutputPipeTrimsAtALineEnd(t *testing.T) {
	p := newOutputPipe()
	line := []byte("\x1b[1;32mgreen text\x1b[0m and more\n")
	for written := 0; written < 2*outputBufferLimit; written += len(line) {
		p.write(line)
	}
	p.end()
	got := readAll(p)
	if !bytes.HasPrefix(got, line) {
		t.Fatalf("output resumes inside a line: %q", got[:len(line)])
	}

	// Without a line end nearby the oldest bytes are cut exactly.
	q := newOutputPipe()
	q.write(bytes.Repeat([]byte("a"), outputBufferLimit))
	q.write([]byte("bc"))
	q.end()
	if got := readAll(q); len(got) != outputBufferLimit || !bytes.HasSuffix(got, []byte("abc")) {
		t.Fatalf("exact cut kept %d bytes", len(got))
	}
}

func TestOutputPipeWriteDoesNotCopyTheBuffer(t *testing.T) {
	p := newOutputPipe()
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 256) // 4 KiB, no line end
	for written := 0; written < 2*outputBufferLimit; written += len(chunk) {
		p.write(chunk)
	}
	if allocs := testing.AllocsPerRun(100, func() { p.write(chunk) }); allocs != 0 {
		t.Fatalf("a write to a full buffer allocates %v times", allocs)
	}
}

func BenchmarkOutputPipeWriteWhenFull(b *testing.B) {
	p := newOutputPipe()
	chunk := bytes.Repeat([]byte("output line of an agent\n"), 160) // 4 KiB
	for written := 0; written < 2*outputBufferLimit; written += len(chunk) {
		p.write(chunk)
	}
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	for b.Loop() {
		p.write(chunk)
	}
}

func TestFindTailOnTheScreen(t *testing.T) {
	tail := promptTail("You are developer-ada.\nRun: omo ai ready  ")
	if tail != "Run: omo ai ready" {
		t.Fatalf("tail %q", tail)
	}
	if got := promptTail(strings.Repeat("x", 40) + "end"); utf8.RuneCountInString(got) != promptTailLength {
		t.Fatalf("long tail %q", got)
	}
	screen := "❯ Run: omo ai ready   \n          \n> Run: omo ai ready     \n"
	if at, ok := findTail(screen, tail); !ok || at != (screenSpot{row: 2, col: 2}) {
		t.Fatalf("last occurrence at %+v %v", at, ok)
	}
	// A line wrapped at the screen edge is found across the rows.
	wrapped := "xxxxxxRun: omo\n ai ready \n"
	if at, ok := findTail(wrapped, tail); !ok || at != (screenSpot{row: 0, col: 6}) {
		t.Fatalf("wrapped at %+v %v", at, ok)
	}
	if _, ok := findTail("nothing here\n", tail); ok {
		t.Fatal("found text that is not there")
	}
}

// stuckInput is a CLI whose screen keeps the typed prompt in its input line
// until it has seen enough Enter presses.
type stuckInput struct {
	*fakeTerminal
	entersNeeded int
	enters       int
}

func (s *stuckInput) SendPromptShown(text string, shown func() bool, _ time.Duration) (bool, error) {
	_ = s.fakeTerminal.SendPrompt(text)
	s.setScreen("❯ " + text + "\n")
	return shown(), s.SendSubmit()
}

func (s *stuckInput) SendSubmit() error {
	s.mu.Lock()
	s.enters++
	if s.enters >= s.entersNeeded {
		s.screen = "\n> " + s.prompts[0] + "\n✻ Working\n❯ \n"
	}
	s.mu.Unlock()
	return nil
}

func (s *stuckInput) setScreen(v string) {
	s.mu.Lock()
	s.screen = v
	s.mu.Unlock()
}

func TestStartPromptEnterPressedAgainWhileTheTextSits(t *testing.T) {
	d := Delivery{Inject: true, RetryWait: time.Millisecond, Retries: 3, SubmitCheck: time.Millisecond, SubmitRetries: 3}
	for _, c := range []struct{ needed, enters int }{{1, 1}, {2, 2}, {4, 4}, {9, 4}} {
		s := &stuckInput{fakeTerminal: newFakeTerminal(), entersNeeded: c.needed}
		deliverStartPrompt(s, "run omo ai ready", false, d, nil, func() time.Time { return time.Time{} }, time.Now, func(m string) { t.Error(m) })
		_, prompts := s.snapshot()
		// The text stays visible, so the prompt is never typed again.
		if len(prompts) != 1 || s.enters != c.enters {
			t.Fatalf("needs %d Enters: typed %d times, %d Enters, want 1 and %d", c.needed, len(prompts), s.enters, c.enters)
		}
	}
}
