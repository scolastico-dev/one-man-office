package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSplitCommandLinePreservesQuotedArguments(t *testing.T) {
	got, err := splitCommandLine(`omo job create --title "two words" --goal 'quoted goal' --role freelancer`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"omo", "job", "create", "--title", "two words", "--goal", "quoted goal", "--role", "freelancer"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if _, err := splitCommandLine(`omo send "unfinished`); err == nil {
		t.Fatal("unterminated quote accepted")
	}
}

func TestCommandConsoleRunsSelectedIdentityAndLogsOutput(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "ceo-ada", "ceo")
	m.tab = tabCommands
	m.openCommandConsole()
	if m.mode != modeCommandConsole || len(m.commands.identities) != 2 {
		t.Fatalf("console state = %+v", m.commands)
	}
	m.commands.selected = 1
	m.commands.line = "omo job list"
	m.commandExec = func(_, socket, officeDir, identity, line string) commandResultMsg {
		if socket != m.o.Sup.SocketPath || officeDir != m.o.Sup.OfficeDir {
			t.Fatalf("connection = socket %q office %q", socket, officeDir)
		}
		if identity != "ceo-ada" || line != "omo job list" {
			t.Fatalf("execution identity=%q line=%q", identity, line)
		}
		return commandResultMsg{at: time.Now(), identity: identity, command: line, output: "[1] working developer test\n"}
	}

	updated, cmd := m.startCommand()
	m = updated.(model)
	if cmd == nil || !m.commands.running {
		t.Fatal("command did not start asynchronously")
	}
	result := cmd().(commandResultMsg)
	m.finishCommand(result)
	if m.commands.running || len(m.commands.logs) != 1 {
		t.Fatalf("finished console state = %+v", m.commands)
	}
	view := m.viewCommandConsole()
	for _, want := range []string{"ceo-ada", "omo job list", "working developer test"} {
		if !strings.Contains(view, want) {
			t.Errorf("console view missing %q:\n%s", want, view)
		}
	}
}

func TestCommandEnvironmentReplacesInjectedIdentity(t *testing.T) {
	env := commandEnvironment([]string{"PATH=/bin", "OMO_SOCKET=old", "OMO_AGENT_ID=old"}, "new-socket", "developer-ada")
	want := []string{"PATH=/bin", "OMO_SOCKET=new-socket", "OMO_AGENT_ID=developer-ada"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("environment = %#v, want %#v", env, want)
	}
}

func TestCommandsTabEnterOpensConsole(t *testing.T) {
	m := testModel(t)
	m.tab = tabCommands
	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	if got := updated.(model).mode; got != modeCommandConsole {
		t.Fatalf("Enter opened mode %v", got)
	}
}
