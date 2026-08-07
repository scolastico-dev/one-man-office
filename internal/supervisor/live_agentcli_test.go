package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// This compatibility test makes one deliberately tiny model request and is
// opt-in so the normal suite never consumes tokens:
//
//	OMO_LIVE_AGENT_CLI=codex go test ./internal/supervisor -run TestLiveAgentCLIHandshake -count=1
//	OMO_LIVE_AGENT_CLI=gemini go test ./internal/supervisor -run TestLiveAgentCLIHandshake -count=1
func TestLiveAgentCLIHandshake(t *testing.T) {
	providerName := os.Getenv("OMO_LIVE_AGENT_CLI")
	if providerName == "" {
		t.Skip("set OMO_LIVE_AGENT_CLI=codex or gemini to spend one minimal model request")
	}
	provider, err := agentcli.Parse(providerName)
	if err != nil {
		t.Fatal(err)
	}
	if provider == agentcli.Claude {
		t.Skip("Claude uses the established delayed-PTY path covered by trust and handshake tests")
	}
	command, err := exec.LookPath(string(provider))
	if err != nil {
		t.Fatalf("%s is not installed: %v", provider, err)
	}

	args := map[agentcli.Provider][]string{
		agentcli.Codex:  {"--dangerously-bypass-approvals-and-sandbox", "--no-alt-screen"},
		agentcli.Gemini: {"--yolo"},
	}[provider]
	o := newOffice(t, nil)
	o.Sup.Cfg.Models["freelancer"] = config.Profile{
		Provider: provider,
		Cmd:      command,
		Args:     args,
		Env: map[string]string{
			"PATH": filepath.Dir(omoBin) + string(os.PathListSeparator) + os.Getenv("PATH"),
		},
	}

	oldReadyTimeout, oldMaxSpawnRetries := ReadyTimeout, MaxSpawnRetries
	ReadyTimeout, MaxSpawnRetries = 60*time.Second, 0
	t.Cleanup(func() {
		ReadyTimeout, MaxSpawnRetries = oldReadyTimeout, oldMaxSpawnRetries
	})
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir,
		"Compatibility check only. After running omo ready, do not inspect files or perform other work.")
	if err != nil {
		t.Fatal(err)
	}
	sess, ok := o.Sup.Session(name)
	if !ok {
		t.Fatal("spawned session is unavailable")
	}
	ready := func() bool {
		events, err := db.EventsSince(o.DB, 0)
		if err != nil {
			return false
		}
		for _, event := range events {
			if event.Kind == "agent_ready" && event.Agent == name {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && !ready() {
		select {
		case <-sess.Done():
			log, _ := os.ReadFile(filepath.Join(o.Dir, ".omo", "logs", LogName(name)))
			t.Fatalf("%s exited before omo ready: %v\nSCREEN:\n%s\nLOG:\n%s", provider, sess.ExitErr(), sess.Screen(), log)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !ready() {
		log, _ := os.ReadFile(filepath.Join(o.Dir, ".omo", "logs", LogName(name)))
		t.Fatalf("%s did not call omo ready\nSCREEN:\n%s\nLOG:\n%s", provider, sess.Screen(), log)
	}
	if err := o.Sup.KillAgent(name, true); err != nil {
		t.Fatal(err)
	}
}
