package agents

import (
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// DefaultDebounce is how long a notification waits after the last key a
// person typed into the agent's terminal.
const DefaultDebounce = 30 * time.Second

// promptSender is what start-prompt delivery needs from the PTY.
type promptSender interface {
	SendPrompt(text string) error
	Done() <-chan struct{}
}

// deliverStartPrompt types the start prompt after d.Delay unless the launch
// already carried it, then resends it up to d.Retries times, d.RetryWait
// apart, for as long as awaiting reports that the agent has not
// acknowledged it. A prompt passed as a launch argument is resent the same
// way: a CLI that swallowed it gets it typed. It returns when delivery is
// over or the process ended; send errors go to warn.
func deliverStartPrompt(s promptSender, prompt string, launchInjected bool, d Delivery, awaiting func() bool, warn func(string)) {
	if !d.Inject {
		return
	}
	if awaiting == nil {
		awaiting = func() bool { return true }
	}
	wait := func(dur time.Duration) bool {
		if dur <= 0 {
			select {
			case <-s.Done():
				return false
			default:
				return true
			}
		}
		timer := time.NewTimer(dur)
		defer timer.Stop()
		select {
		case <-timer.C:
			return true
		case <-s.Done():
			return false
		}
	}
	if !launchInjected {
		if !wait(d.Delay) || !awaiting() {
			return
		}
		if err := s.SendPrompt(prompt); err != nil {
			warn("typing the start prompt failed: " + err.Error())
		}
	}
	for retry := 1; retry <= d.Retries; retry++ {
		if !wait(d.RetryWait) || !awaiting() {
			return
		}
		if err := s.SendPrompt(prompt); err != nil {
			warn("resending the start prompt failed: " + err.Error())
		}
	}
}

// lineTypist is what notifications need from the PTY: typing a line and
// its Enter only if a condition still holds once omo owns the input.
type lineTypist interface {
	SendTextAndKeysIf(text, keys string, ready func() bool) (bool, error)
	Done() <-chan struct{}
}

// notifier types queued notification lines into a terminal, one at a time
// and in order, each only after nobody typed for the hold-back period.
type notifier struct {
	typist   lineTypist
	input    *inputClock
	now      func() time.Time
	debounce time.Duration // <= 0: no hold-back

	mu    sync.Mutex
	queue []string
	wake  chan struct{}
}

func newNotifier(t lineTypist, input *inputClock, now func() time.Time, debounce time.Duration) *notifier {
	return &notifier{typist: t, input: input, now: now, debounce: debounce, wake: make(chan struct{}, 1)}
}

// enqueue adds a line; lines after the process ended are dropped.
func (n *notifier) enqueue(line string) {
	select {
	case <-n.typist.Done():
		return
	default:
	}
	line = notificationLine(line)
	if line == "" {
		return
	}
	n.mu.Lock()
	n.queue = append(n.queue, line)
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// holdBack is how much longer typing must stay idle; zero means type now.
func (n *notifier) holdBack() time.Duration {
	if n.debounce <= 0 {
		return 0
	}
	last := n.input.lastInput()
	if last.IsZero() {
		return 0
	}
	return max(n.debounce-n.now().Sub(last), 0)
}

// flush types queued lines while typing is idle. It returns how long to
// wait before the next attempt, or 0 when the queue is empty.
func (n *notifier) flush() time.Duration {
	for {
		n.mu.Lock()
		if len(n.queue) == 0 {
			n.mu.Unlock()
			return 0
		}
		line := n.queue[0]
		n.mu.Unlock()
		if wait := n.holdBack(); wait > 0 {
			return wait
		}
		// The idle check runs again while omo holds the input, so a key a
		// person types in between cannot end up inside the line.
		sent, err := n.typist.SendTextAndKeysIf(line, "\r", func() bool { return n.holdBack() == 0 })
		if !sent && err == nil {
			continue
		}
		n.mu.Lock()
		n.queue = n.queue[1:]
		n.mu.Unlock()
		if err != nil {
			// The terminal is gone; nothing queued can be typed any more.
			n.drop()
			return 0
		}
	}
}

func (n *notifier) drop() {
	n.mu.Lock()
	n.queue = nil
	n.mu.Unlock()
}

// run delivers until the process ends.
func (n *notifier) run() {
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		n.drop()
	}()
	for {
		select {
		case <-n.typist.Done():
			return
		case <-n.wake:
		case <-timerC:
			timerC = nil
		}
		if timerC != nil {
			continue // a retry is already scheduled
		}
		if wait := n.flush(); wait > 0 {
			if timer == nil {
				timer = time.NewTimer(wait)
			} else {
				timer.Reset(wait)
			}
			timerC = timer.C
		}
	}
}

// NotifyPrefix starts every notification line. An agent CLI takes a line
// beginning with "/", "!" or "#" as a command of its own; behind the prefix
// no line does.
const NotifyPrefix = "omo: "

// MaxNotifyLength caps a notification line, prefix included, in characters.
const MaxNotifyLength = 1000

// notificationLine makes text safe to type as a notification: one line (see
// singleLine) that starts with NotifyPrefix exactly once and is at most
// MaxNotifyLength characters, cut with an ellipsis. Empty text yields "".
func notificationLine(text string) string {
	body := singleLine(text)
	if rest, ok := strings.CutPrefix(body, strings.TrimSpace(NotifyPrefix)); ok {
		body = strings.TrimSpace(rest)
	}
	if body == "" {
		return ""
	}
	line := NotifyPrefix + body
	if utf8.RuneCountInString(line) <= MaxNotifyLength {
		return line
	}
	cut, count := 0, 0
	for i := range line {
		if count == MaxNotifyLength-1 {
			cut = i
			break
		}
		count++
	}
	return line[:cut] + "…"
}

// singleLine makes text safe to type as one line: everything a terminal or
// a CLI may treat as a line break (CR, LF, VT, FF, NEL, the Unicode line
// and paragraph separators) and tabs become spaces, other control
// characters (escape sequences among them) are dropped, and runs of spaces
// collapse.
func singleLine(text string) string {
	var b strings.Builder
	space := false
	for _, r := range text {
		switch {
		case isLineBreak(r) || r == '\t' || unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029', '\u001c', '\u001d', '\u001e':
		return true
	}
	return unicode.In(r, unicode.Zl, unicode.Zp)
}
