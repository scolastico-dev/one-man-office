package supervisor

import (
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

type recordingPromptSender struct {
	calls  int
	onSend func()
	done   chan struct{}
}

func (s *recordingPromptSender) SendPrompt(string) error {
	s.calls++
	if s.onSend != nil {
		s.onSend()
	}
	return nil
}

func (s *recordingPromptSender) Done() <-chan struct{} {
	if s.done == nil {
		s.done = make(chan struct{})
	}
	return s.done
}

func promptProfile(inject bool, retries int) config.Profile {
	delay := config.Duration(0)
	wait := config.Duration(time.Millisecond)
	return config.Profile{
		InjectPrompt: &inject, PromptDelay: &delay, PromptRetryCount: &retries, PromptRetryWait: &wait,
	}
}

func TestInitialPromptRetriesWhileAgentIsNotReady(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-retry", Role: "developer", Profile: "test"}); err != nil {
		t.Fatal(err)
	}
	sender := &recordingPromptSender{}
	o.Sup.deliverInitialPrompt("developer-retry", 0, sender, "start", false, promptProfile(true, 2))
	if sender.calls != 3 {
		t.Fatalf("prompt injections = %d, want initial + 2 retries", sender.calls)
	}
}

func TestInitialPromptRetryStopsAfterReady(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-ready", Role: "developer", Profile: "test"}); err != nil {
		t.Fatal(err)
	}
	sender := &recordingPromptSender{onSend: func() {
		_ = db.SetAgentState(o.DB, "developer-ready", "working")
	}}
	o.Sup.deliverInitialPrompt("developer-ready", 0, sender, "start", false, promptProfile(true, 3))
	if sender.calls != 1 {
		t.Fatalf("prompt injections after ready = %d, want 1", sender.calls)
	}
}

func TestInitialPromptInjectionCanBeDisabledIndependently(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-manual", Role: "developer", Profile: "test"}); err != nil {
		t.Fatal(err)
	}
	sender := &recordingPromptSender{}
	o.Sup.deliverInitialPrompt("developer-manual", 0, sender, "start", true, promptProfile(false, 3))
	if sender.calls != 0 {
		t.Fatalf("disabled injection sent %d prompts", sender.calls)
	}
}

func TestArgumentPromptStillGetsConfiguredRetries(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-argument", Role: "developer", Profile: "test"}); err != nil {
		t.Fatal(err)
	}
	sender := &recordingPromptSender{}
	o.Sup.deliverInitialPrompt("developer-argument", 0, sender, "start", true, promptProfile(true, 2))
	if sender.calls != 2 {
		t.Fatalf("argument prompt retries = %d, want 2", sender.calls)
	}
}

func TestZeroPromptRetriesPreservesOneShotDelivery(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-once", Role: "developer", Profile: "test"}); err != nil {
		t.Fatal(err)
	}
	sender := &recordingPromptSender{}
	o.Sup.deliverInitialPrompt("developer-once", 0, sender, "start", false, promptProfile(true, 0))
	if sender.calls != 1 {
		t.Fatalf("zero retries sent %d prompts, want 1", sender.calls)
	}
}
