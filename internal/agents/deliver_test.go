package agents

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeTerminal stands in for an agent PTY: it records what omo types.
type fakeTerminal struct {
	mu      sync.Mutex
	typed   []string
	prompts []string
	done    chan struct{}
	exitErr error
	failing bool
}

func newFakeTerminal() *fakeTerminal { return &fakeTerminal{done: make(chan struct{})} }

func (f *fakeTerminal) SendTextAndKeysIf(text, keys string, ready func() bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ready() {
		return false, nil
	}
	if f.failing {
		return true, errors.New("terminal closed")
	}
	f.typed = append(f.typed, text+keys)
	return true, nil
}

func (f *fakeTerminal) SendPrompt(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, text)
	return nil
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
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before the deadline")
		}
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

	held := newNotifier(term, input, now, time.Hour)
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
	immediate := newNotifier(direct, dInput, now, 0)
	immediate.enqueue("omo: x")
	if wait := immediate.flush(); wait != 0 {
		t.Fatalf("no hold-back configured, flush waited %v", wait)
	}
	if got, _ := direct.snapshot(); len(got) != 2 || got[1] != "omo: x\r" {
		t.Fatalf("typed = %q", got)
	}
}

func TestNotifyRunsInOrderAndStopsAtExit(t *testing.T) {
	term := newFakeTerminal()
	n := newNotifier(term, &inputClock{}, time.Now, time.Millisecond)
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
	n := newNotifier(term, &inputClock{}, time.Now, 0)
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

func (l *lateAgent) SendPrompt(text string) error {
	_ = l.fakeTerminal.SendPrompt(text)
	_, prompts := l.snapshot()
	if l.printsAfter > 0 && len(prompts) >= l.printsAfter {
		l.mu.Lock()
		l.printing = true
		l.mu.Unlock()
	}
	return nil
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

func TestOutputPipeNeverBlocksTheWriter(t *testing.T) {
	p := newOutputPipe()
	chunk := make([]byte, 64<<10)
	for i := 0; i < 64; i++ { // 4 MiB with nobody reading
		p.write(chunk)
	}
	if len(p.buf) > outputBufferLimit {
		t.Fatalf("buffer grew to %d bytes", len(p.buf))
	}
	p.end()
	total := 0
	buf := make([]byte, 32<<10)
	for {
		n, err := p.Read(buf)
		total += n
		if err != nil {
			break
		}
	}
	if total != outputBufferLimit {
		t.Fatalf("read %d bytes after end, want the last %d", total, outputBufferLimit)
	}
}
