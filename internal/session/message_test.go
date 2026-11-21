package session

import (
	"reflect"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/hinshun/vt10x"
)

// A gated image read reproduces a CLI that accepts a paste now and inserts its
// image later. Advancing the message before that insertion loses its position.
func TestMessageWaitsForImageBeforeFollowingTextAndSubmit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &recordingProcess{}
		s := &Session{proc: p, term: vt10x.New(vt10x.WithSize(100, 10)), done: make(chan struct{}), subs: make(map[chan struct{}]struct{})}
		s.opts.PasteReady = func(pasted, before string) func(string) bool {
			if !strings.HasSuffix(pasted, ".png") {
				return nil
			}
			return func(screen string) bool { return strings.Count(screen, "[Image]") > strings.Count(before, "[Image]") }
		}
		finished := make(chan error, 1)
		done := make(chan struct{})
		t.Cleanup(func() { close(s.done); <-done })
		go func() {
			defer close(done)
			finished <- s.SendPartsAndKeys([]string{"right: ", "/one.png", " left: ", "/two.png", " top: ", "/three.png"}, "\r")
		}()
		for i, want := range [][]string{
			{"right: ", "/one.png"},
			{"right: ", "/one.png", " left: ", "/two.png"},
			{"right: ", "/one.png", " left: ", "/two.png", " top: ", "/three.png"},
		} {
			synctest.Wait()
			if got := p.writes; !reflect.DeepEqual(got, bytesOf(want)) {
				t.Fatalf("while image %d is loading: writes = %q, want %q", i+1, got, want)
			}
			if s.inputMu.TryLock() {
				s.inputMu.Unlock()
				t.Fatal("another writer can split the draft while an image loads")
			}
			// A device query must still be answered during the wait: otherwise
			// the output pump can block before publishing the image's screen.
			s.writeInput = func([]byte) {}
			s.answerQueries(queryDSR)
			// An unrelated redraw is not confirmation of the image.
			s.notify()
			synctest.Wait()
			if !reflect.DeepEqual(p.writes, bytesOf(want)) {
				t.Fatal("an unrelated redraw released the paste")
			}
			s.show([]byte("[Image]"))
			s.notify()
		}
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if got := string(p.writes[len(p.writes)-1]); got != "\r" {
			t.Fatalf("last input = %q, want submit", got)
		}
	})
}

func TestUnconfirmedImageStopsTheMessageWithoutSubmitting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &recordingProcess{}
		s := &Session{proc: p, term: vt10x.New(), done: make(chan struct{}), subs: make(map[chan struct{}]struct{})}
		s.opts.PasteReady = func(string, string) func(string) bool { return func(string) bool { return false } }
		if err := s.SendPartsAndKeys([]string{"/one.png", "tail"}, "\r"); err == nil {
			t.Fatal("unconfirmed paste reported success")
		}
		if !reflect.DeepEqual(p.writes, bytesOf([]string{"/one.png"})) {
			t.Fatalf("unconfirmed image was followed by more input: %q", p.writes)
		}
	})
}

func TestProcessExitEndsImageWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &recordingProcess{}
		s := &Session{proc: p, term: vt10x.New(), done: make(chan struct{}), subs: make(map[chan struct{}]struct{})}
		s.opts.PasteReady = func(string, string) func(string) bool { return func(string) bool { return false } }
		finished := make(chan error, 1)
		go func() { finished <- s.SendPartsAndKeys([]string{"/one.png", "tail"}, "\r") }()
		synctest.Wait()
		close(s.done)
		if err := <-finished; err == nil {
			t.Fatal("process exit reported success")
		}
		if len(p.writes) != 1 {
			t.Fatalf("input continued after process exit: %q", p.writes)
		}
	})
}

func bytesOf(parts []string) [][]byte {
	var out [][]byte
	for _, p := range parts {
		out = append(out, []byte(p))
	}
	return out
}
