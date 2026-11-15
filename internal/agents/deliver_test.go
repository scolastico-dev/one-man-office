package agents

import (
	"errors"
	"sync"
	"testing"
	"time"
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

func TestStartPromptDeliveryRules(t *testing.T) {
	quick := Delivery{Inject: true, Delay: 0, RetryWait: time.Millisecond, Retries: 2}

	typed := newFakeTerminal()
	deliverStartPrompt(typed, "run omo ai ready", false, quick, nil, func(m string) { t.Error(m) })
	if _, prompts := typed.snapshot(); len(prompts) != 3 {
		t.Fatalf("never acknowledged: %d sends, want 1 + 2 retries", len(prompts))
	}

	acked := newFakeTerminal()
	deliverStartPrompt(acked, "p", false, quick, func() bool { _, p := acked.snapshot(); return len(p) == 0 }, func(m string) { t.Error(m) })
	if _, prompts := acked.snapshot(); len(prompts) != 1 {
		t.Fatalf("acknowledged after the first send: %d sends", len(prompts))
	}

	launched := newFakeTerminal()
	deliverStartPrompt(launched, "p", true, quick, nil, func(m string) { t.Error(m) })
	if _, prompts := launched.snapshot(); len(prompts) != 2 {
		t.Fatalf("launch argument: %d resends, want 2", len(prompts))
	}

	off := newFakeTerminal()
	deliverStartPrompt(off, "p", false, Delivery{Retries: 3}, nil, func(m string) { t.Error(m) })
	if _, prompts := off.snapshot(); len(prompts) != 0 {
		t.Fatalf("inject off: %d sends", len(prompts))
	}

	ended := newFakeTerminal()
	close(ended.done)
	deliverStartPrompt(ended, "p", false, Delivery{Inject: true, Delay: time.Hour, Retries: 3, RetryWait: time.Hour}, nil, func(m string) { t.Error(m) })
	if _, prompts := ended.snapshot(); len(prompts) != 0 {
		t.Fatalf("after exit: %d sends", len(prompts))
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
