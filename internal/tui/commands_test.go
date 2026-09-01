package tui

import (
	"fmt"
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

func TestCommandConsoleSplitsSpaceWithReadableHistory(t *testing.T) {
	m := testModel(t)
	m.openCommandConsole()
	output := make([]string, 20)
	for i := range output {
		output[i] = fmt.Sprintf("output line %02d", i+1)
	}
	m.commands.logs = []commandLog{{
		at: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), identity: "user",
		command: "omo logs developer-ada", output: strings.Join(output, "\n"),
	}}

	view := m.viewCommandConsole()
	lines := strings.Split(view, "\n")
	if len(lines) != m.h {
		t.Fatalf("console has %d rows, want %d", len(lines), m.h)
	}
	historyRow := -1
	for i, line := range lines {
		if strings.Contains(line, "Command history") {
			historyRow = i
			break
		}
	}
	if historyRow != 15 {
		t.Fatalf("history begins on row %d, want lower half at row 15:\n%s", historyRow, view)
	}
	for _, want := range []string{"omo logs developer-ada", "output line 20"} {
		if !strings.Contains(view, want) {
			t.Errorf("history missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "output line 01") {
		t.Fatalf("history kept the oldest output instead of the newest tail:\n%s", view)
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

func TestCommandCatalogCoversCoreOperationsAndKeepsRawLast(t *testing.T) {
	catalog := commandCatalog()
	paths := map[string]bool{}
	for _, spec := range catalog {
		paths[spec.Path] = true
	}
	for _, want := range []string{"send", "job create", "job cancel", "agent restart", "incident resolve", "office pause", "safe-shutdown", "context save", "logs", "type", "repo add", "reload"} {
		if !paths[want] {
			t.Errorf("catalog missing %q", want)
		}
	}
	if len(catalog) == 0 || !catalog[len(catalog)-1].Raw || catalog[len(catalog)-1].Title != "Advanced: raw command" {
		t.Fatalf("raw command is not the final advanced item: %#v", catalog)
	}
}

func TestBuildGuidedCommandValidatesAndQuotesInputs(t *testing.T) {
	spec := commandSpec{Path: "job create", Inputs: []commandInput{
		input("title", "title", "", true), input("goal", "goal", "", true), boolean("force", "force", ""),
	}}
	if _, err := buildGuidedCommand(spec, []string{"title only"}); err == nil {
		t.Fatal("missing required goal was accepted")
	}
	got, err := buildGuidedCommand(spec, []string{"two words", "it's ready", "true"})
	if err != nil {
		t.Fatal(err)
	}
	want := `omo job create --title 'two words' --goal 'it'"'"'s ready' --force`
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestGuidedCommandShowsHelpInputsAndChoices(t *testing.T) {
	m := testModel(t)
	m.openCommandConsole()
	for i, spec := range m.commands.catalog {
		if spec.Path == "job create" {
			m.commands.item = i
			break
		}
	}
	updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.commands.screen != commandForm {
		t.Fatalf("screen = %v, want form", m.commands.screen)
	}
	updated, _ = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	updated, _ = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	view := m.viewCommandConsole()
	for _, want := range []string{"Job: create", "title *", "goal *", "role *", "product_manager | developer | freelancer"} {
		if !strings.Contains(view, want) {
			t.Errorf("form missing %q:\n%s", want, view)
		}
	}
}

func TestDestructiveGuidedCommandRequiresTypedConfirmation(t *testing.T) {
	m := testModel(t)
	m.openCommandConsole()
	for i, spec := range m.commands.catalog {
		if spec.Path == "estop" {
			m.commands.item = i
			break
		}
	}
	updated, cmd := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil || m.commands.screen != commandConfirm || m.commands.line != "omo estop" {
		t.Fatalf("confirmation state = %+v cmd=%v", m.commands, cmd)
	}
	updated, cmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil || !strings.Contains(m.commands.status, "type yes") {
		t.Fatalf("unconfirmed command ran: state=%+v cmd=%v", m.commands, cmd)
	}
}

func TestAdvancedRawCommandRemainsAvailable(t *testing.T) {
	m := testModel(t)
	m.openCommandConsole()
	m.commands.item = len(m.commands.catalog) - 1
	updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.commands.screen != commandRaw || !strings.Contains(m.viewCommandConsole(), "Advanced: raw command") {
		t.Fatalf("advanced raw mode unavailable: %+v", m.commands)
	}
}
