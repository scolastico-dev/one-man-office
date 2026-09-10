package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

func TestCommandCatalogRowClickSelectsThenOpensVisibleNonZeroViewport(t *testing.T) {
	m := testModel(t)
	m.w, m.h = 160, 16
	m.openCommandConsole()
	m.commands.item = 8
	view := m.View()
	lines := strings.Split(ansi.Strip(view), "\n")
	row := -1
	for i, line := range lines {
		if strings.Contains(line, "Job: create") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("visible non-selected catalog row missing:\n%s", ansi.Strip(view))
	}
	x, ok := findRenderedCell(view, row, "Job: create")
	if !ok {
		t.Fatalf("catalog row cell missing:\n%s", ansi.Strip(view))
	}

	selected := updateMouse(m, x, row, tea.MouseButtonLeft, tea.MouseActionPress)
	if selected.commands.item != 7 || selected.commands.screen != commandBrowse {
		t.Fatalf("first row click state = item %d screen %d, want item 7 browse", selected.commands.item, selected.commands.screen)
	}

	view = selected.View()
	row = -1
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "Job: create") {
			row = i
			break
		}
	}
	x, ok = findRenderedCell(view, row, "Job: create")
	if !ok {
		t.Fatalf("selected catalog row cell missing:\n%s", ansi.Strip(view))
	}
	opened := updateMouse(selected, x, row, tea.MouseButtonLeft, tea.MouseActionPress)
	keyed, keyCmd := selected.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
	if keyCmd != nil || !reflect.DeepEqual(opened.commands, keyed.(model).commands) {
		t.Fatalf("second row click state = %+v cmd=%v, Enter state = %+v cmd=%v", opened.commands, nil, keyed.(model).commands, keyCmd)
	}
}

func TestCommandFormClicksFocusInputAndReplaceVisibleSuggestions(t *testing.T) {
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
	original := append([]string(nil), m.commands.values...)
	view := m.View()
	goalRow := -1
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "goal *") {
			goalRow = i
			break
		}
	}
	if goalRow < 0 {
		t.Fatalf("goal input is not visible:\n%s", ansi.Strip(view))
	}
	goalX, ok := findRenderedCell(view, goalRow, "goal *")
	if !ok {
		t.Fatalf("goal input cell is not visible:\n%s", ansi.Strip(view))
	}
	focused := updateMouse(m, goalX, goalRow, tea.MouseButtonLeft, tea.MouseActionPress)
	if focused.commands.input != 1 || !reflect.DeepEqual(focused.commands.values, original) {
		t.Fatalf("input click state = input %d values %#v, want input 1 and unchanged values %#v", focused.commands.input, focused.commands.values, original)
	}

	focused.commands.input = 2
	focused.commands.values[2] = "custom value"
	view = focused.View()
	choiceRow := -1
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "choices: product_manager | developer | freelancer") {
			choiceRow = i
			break
		}
	}
	if choiceRow < 0 {
		t.Fatalf("role suggestions are not visible:\n%s", ansi.Strip(view))
	}
	for _, choice := range []string{"product_manager", "developer", "freelancer"} {
		view = focused.View()
		x, ok := findRenderedCell(view, choiceRow, choice)
		if !ok {
			t.Fatalf("suggestion %q is not visible:\n%s", choice, ansi.Strip(view))
		}
		focused = updateMouse(focused, x, choiceRow, tea.MouseButtonLeft, tea.MouseActionPress)
		if focused.commands.input != 2 || focused.commands.values[2] != choice {
			t.Fatalf("suggestion %q click state = input %d value %q", choice, focused.commands.input, focused.commands.values[2])
		}
	}
}

func TestRawCommandClicksFocusFieldsAndUseIdentityArrowBehavior(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "ceo-ada", "ceo")
	m.openCommandConsole()
	m.commands.item = len(m.commands.catalog) - 1
	updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	view := m.View()
	identityRow, commandRow := -1, -1
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "Identity:") {
			identityRow = i
		}
		if strings.Contains(line, "Command:") {
			commandRow = i
		}
	}
	if identityRow < 0 || commandRow < 0 {
		t.Fatalf("raw fields are not visible:\n%s", ansi.Strip(view))
	}
	identityX, _ := findRenderedCell(view, identityRow, "Identity:")
	m = updateMouse(m, identityX, identityRow, tea.MouseButtonLeft, tea.MouseActionPress)
	if m.commands.field != commandIdentity {
		t.Fatalf("identity click selected field %d, want identity", m.commands.field)
	}
	view = m.View()
	commandX, _ := findRenderedCell(view, commandRow, "Command:")
	m = updateMouse(m, commandX, commandRow, tea.MouseButtonLeft, tea.MouseActionPress)
	if m.commands.field != commandLine {
		t.Fatalf("command click selected field %d, want command line", m.commands.field)
	}
	view = m.View()
	identityX, _ = findRenderedCell(view, identityRow, "Identity:")
	m = updateMouse(m, identityX, identityRow, tea.MouseButtonLeft, tea.MouseActionPress)
	view = m.View()
	leftX, ok := findRenderedCell(view, identityRow, "←")
	if !ok {
		t.Fatalf("raw identity arrow is not visible:\n%s", ansi.Strip(view))
	}
	selected := m.commands.selected
	m = updateMouse(m, leftX, identityRow, tea.MouseButtonLeft, tea.MouseActionPress)
	if m.commands.field != commandIdentity || m.commands.selected != (selected+len(m.commands.identities)-1)%len(m.commands.identities) {
		t.Fatalf("identity arrow click state = field %d selected %d", m.commands.field, m.commands.selected)
	}
}

func clickCommandFooter(t *testing.T, m model, hint string) (model, tea.Cmd) {
	t.Helper()
	view := m.View()
	x, ok := findRenderedCell(view, m.h-1, hint)
	if !ok {
		t.Fatalf("footer hint %q is not visible:\n%s", hint, ansi.Strip(view))
	}
	updated, cmd := m.Update(tea.MouseMsg{X: x, Y: m.h - 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return updated.(model), cmd
}

func TestCommandIdentityArrowClicksSelectOnEveryScreen(t *testing.T) {
	for _, screen := range []commandScreen{commandBrowse, commandForm, commandConfirm, commandRaw} {
		t.Run(fmt.Sprintf("screen-%d", screen), func(t *testing.T) {
			m := testModel(t)
			addLivingAgent(t, m, "ceo-ada", "ceo")
			m.openCommandConsole()
			switch screen {
			case commandForm:
				for i, spec := range m.commands.catalog {
					if spec.Path == "job create" {
						m.commands.item = i
						break
					}
				}
				updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
				m = updated.(model)
			case commandConfirm:
				for i, spec := range m.commands.catalog {
					if spec.Path == "estop" {
						m.commands.item = i
						break
					}
				}
				updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
				m = updated.(model)
			case commandRaw:
				m.commands.item = len(m.commands.catalog) - 1
				updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
				m = updated.(model)
			}
			view := m.View()
			x, ok := findRenderedCell(view, 1, "←")
			if !ok {
				t.Fatalf("top identity arrow is not visible:\n%s", ansi.Strip(view))
			}
			updated, cmd := m.Update(tea.MouseMsg{X: x, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			if cmd != nil || updated.(model).commands.selected != 1 {
				t.Fatalf("identity arrow click selected=%d cmd=%v, want selected 1", updated.(model).commands.selected, cmd)
			}
		})
	}
}

func TestRunningCommandFormChoiceClickCannotEditLockedValue(t *testing.T) {
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
	m.commands.input = 2
	m.commands.values[2] = "product_manager"
	m.commands.running = true
	before := append([]string(nil), m.commands.values...)
	clicked, clickCmd := clickCommandFooter(t, m, "←/→ choice")
	if clickCmd != nil || !reflect.DeepEqual(clicked.commands.values, before) {
		t.Fatalf("running choice click values=%#v cmd=%v, want unchanged %#v and nil cmd", clicked.commands.values, clickCmd, before)
	}
}

func TestClippedCommandFooterHintHasNoHit(t *testing.T) {
	m := testModel(t)
	m.w, m.h = 20, 16
	m.openCommandConsole()
	view := m.View()
	if _, ok := findRenderedCell(view, m.h-1, "Enter open"); ok {
		t.Fatalf("Enter open unexpectedly fits clipped footer:\n%s", ansi.Strip(view))
	}
	for _, rect := range m.hitMap.rects {
		if rect.y == m.h-1 {
			if key, ok := rect.action.(keyAction); ok && key.key.Type == tea.KeyEnter {
				t.Fatalf("clipped Enter open retained a hit: %+v", rect)
			}
		}
	}
}

func TestObserverCommandsRouteCannotInvokeExecutor(t *testing.T) {
	m := testModel(t)
	m.observer = true
	m.commandExec = func(_, _, _, _, _ string) commandResultMsg {
		t.Fatal("observer invoked command executor")
		return commandResultMsg{}
	}
	view := m.View()
	if strings.Contains(ansi.Strip(view), "Commands") {
		t.Fatalf("observer exposed Commands:\n%s", ansi.Strip(view))
	}
	updated, cmd := m.updateClick(rowAction{tab: tabCommands, row: 0})
	if cmd != nil || updated.(model).mode == modeCommandConsole {
		t.Fatalf("observer command row route changed mode=%d cmd=%v", updated.(model).mode, cmd)
	}
}

func TestCommandFooterHintsUseEquivalentKeyboardRoutes(t *testing.T) {
	t.Run("browse help and open", func(t *testing.T) {
		m := testModel(t)
		addLivingAgent(t, m, "ceo-ada", "ceo")
		m.openCommandConsole()
		clicked, clickCmd := clickCommandFooter(t, m, "? help")
		keyed, keyCmd := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.commands.showHelp != keyed.(model).commands.showHelp {
			t.Fatalf("help click showHelp=%v cmd=%v, key showHelp=%v cmd=%v", clicked.commands.showHelp, clickCmd, keyed.(model).commands.showHelp, keyCmd)
		}

		m.commands.showHelp = false
		clicked, clickCmd = clickCommandFooter(t, m, "Enter open")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.mode != keyed.(model).mode || !reflect.DeepEqual(clicked.commands, keyed.(model).commands) {
			t.Fatalf("open click state=%+v cmd=%v, key state=%+v cmd=%v", clicked.commands, clickCmd, keyed.(model).commands, keyCmd)
		}

		m.commands.showHelp = false
		clicked, clickCmd = clickCommandFooter(t, m, "↑/↓ choose")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyDown})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.commands.item != keyed.(model).commands.item {
			t.Fatalf("choose click item=%d cmd=%v, key item=%d cmd=%v", clicked.commands.item, clickCmd, keyed.(model).commands.item, keyCmd)
		}

		m.commands.item = 0
		m.commands.selected = 0
		clicked, clickCmd = clickCommandFooter(t, m, "←/→ identity")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyRight})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.commands.selected != keyed.(model).commands.selected {
			t.Fatalf("identity click selected=%d cmd=%v, key selected=%d cmd=%v", clicked.commands.selected, clickCmd, keyed.(model).commands.selected, keyCmd)
		}
	})

	t.Run("form run and back", func(t *testing.T) {
		m := testModel(t)
		m.openCommandConsole()
		for i, spec := range m.commands.catalog {
			if spec.Path == "done" {
				m.commands.item = i
				break
			}
		}
		updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(model)
		m.commandExec = func(_, _, _, _, line string) commandResultMsg {
			return commandResultMsg{command: line}
		}
		clicked, clickCmd := clickCommandFooter(t, m, "Enter run")
		keyed, keyCmd := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if clickCmd == nil || keyCmd == nil || clicked.commands.running != keyed.(model).commands.running || clicked.commands.status != keyed.(model).commands.status {
			t.Fatalf("run click running=%v status=%q cmd=%v, key running=%v status=%q cmd=%v", clicked.commands.running, clicked.commands.status, clickCmd, keyed.(model).commands.running, keyed.(model).commands.status, keyCmd)
		}

		m.commands.running = false
		m.commands.screen = commandForm
		m.commands.input = 0
		clicked, clickCmd = clickCommandFooter(t, m, "Tab/↑/↓ field")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyTab})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.commands.input != keyed.(model).commands.input {
			t.Fatalf("field click input=%d cmd=%v, key input=%d cmd=%v", clicked.commands.input, clickCmd, keyed.(model).commands.input, keyCmd)
		}

		m.commands.input = 0
		clicked, clickCmd = clickCommandFooter(t, m, "←/→ choice")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyRight})
		if (clickCmd == nil) != (keyCmd == nil) || !reflect.DeepEqual(clicked.commands.values, keyed.(model).commands.values) {
			t.Fatalf("choice click values=%#v cmd=%v, key values=%#v cmd=%v", clicked.commands.values, clickCmd, keyed.(model).commands.values, keyCmd)
		}

		clicked, clickCmd = clickCommandFooter(t, m, "Esc back")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEsc})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.commands.screen != keyed.(model).commands.screen {
			t.Fatalf("back click screen=%d cmd=%v, key screen=%d cmd=%v", clicked.commands.screen, clickCmd, keyed.(model).commands.screen, keyCmd)
		}
	})

	t.Run("confirm and cancel", func(t *testing.T) {
		m := testModel(t)
		m.openCommandConsole()
		for i, spec := range m.commands.catalog {
			if spec.Path == "estop" {
				m.commands.item = i
				break
			}
		}
		updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(model)
		m.commands.confirm = "yes"
		m.commandExec = func(_, _, _, _, line string) commandResultMsg { return commandResultMsg{command: line} }
		clicked, clickCmd := clickCommandFooter(t, m, "Enter confirm")
		keyed, keyCmd := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if clickCmd == nil || keyCmd == nil || clicked.commands.running != keyed.(model).commands.running || clicked.commands.screen != keyed.(model).commands.screen {
			t.Fatalf("confirm click screen=%d running=%v cmd=%v, key screen=%d running=%v cmd=%v", clicked.commands.screen, clicked.commands.running, clickCmd, keyed.(model).commands.screen, keyed.(model).commands.running, keyCmd)
		}

		m.commands.running = false
		m.commands.screen = commandConfirm
		m.commands.confirm = ""
		clicked, clickCmd = clickCommandFooter(t, m, "Esc cancel")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEsc})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.commands.screen != keyed.(model).commands.screen || clicked.commands.confirm != keyed.(model).commands.confirm {
			t.Fatalf("cancel click state=%+v cmd=%v, key state=%+v cmd=%v", clicked.commands, clickCmd, keyed.(model).commands, keyCmd)
		}
	})

	t.Run("raw field and run back", func(t *testing.T) {
		m := testModel(t)
		m.openCommandConsole()
		m.commands.item = len(m.commands.catalog) - 1
		updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(model)
		m.commands.line = "omo job list"
		m.commandExec = func(_, _, _, _, line string) commandResultMsg { return commandResultMsg{command: line} }
		clicked, clickCmd := clickCommandFooter(t, m, "Tab field")
		keyed, keyCmd := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyTab})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.commands.field != keyed.(model).commands.field {
			t.Fatalf("raw field click field=%d cmd=%v, key field=%d cmd=%v", clicked.commands.field, clickCmd, keyed.(model).commands.field, keyCmd)
		}

		m.commands.field = commandLine
		clicked, clickCmd = clickCommandFooter(t, m, "Enter run")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if clickCmd == nil || keyCmd == nil || clicked.commands.running != keyed.(model).commands.running {
			t.Fatalf("raw run click running=%v cmd=%v, key running=%v cmd=%v", clicked.commands.running, clickCmd, keyed.(model).commands.running, keyCmd)
		}

		m.commands.running = false
		clicked, clickCmd = clickCommandFooter(t, m, "Esc back")
		keyed, keyCmd = m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEsc})
		if (clickCmd == nil) != (keyCmd == nil) || clicked.commands.screen != keyed.(model).commands.screen {
			t.Fatalf("raw back click screen=%d cmd=%v, key screen=%d cmd=%v", clicked.commands.screen, clickCmd, keyed.(model).commands.screen, keyCmd)
		}
	})
}

func TestCommandClicksRespectRunningAndClippingGuards(t *testing.T) {
	m := testModel(t)
	m.w, m.h = 20, 16
	m.openCommandConsole()
	for i, spec := range m.commands.catalog {
		if spec.Path == "job create" {
			m.commands.item = i
			break
		}
	}
	updated, _ := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	m.commands.input = 2
	m.commands.values[2] = "unchanged"
	_ = m.View()
	for _, rect := range m.hitMap.rects {
		if _, ok := rect.action.(suggestionAction); ok {
			t.Fatalf("clipped suggestion retained a hit: %+v", rect)
		}
	}
	m.commands.running = true
	view := m.View()
	goalRow := -1
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "goal *") {
			goalRow = i
			break
		}
	}
	if goalRow < 0 {
		t.Fatalf("goal input disappeared:\n%s", ansi.Strip(view))
	}
	x, ok := findRenderedCell(view, goalRow, "goal *")
	if !ok {
		t.Fatalf("goal input cell disappeared:\n%s", ansi.Strip(view))
	}
	before := append([]string(nil), m.commands.values...)
	m = updateMouse(m, x, goalRow, tea.MouseButtonLeft, tea.MouseActionPress)
	if !reflect.DeepEqual(m.commands.values, before) {
		t.Fatalf("running input click edited values %#v, want %#v", m.commands.values, before)
	}
	if _, cmd := m.updateCommandConsole(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("running command accepted a duplicate execution")
	}
}

func TestCommandConsoleIgnoresHistoryAndNonLeftMouseEvents(t *testing.T) {
	m := testModel(t)
	m.openCommandConsole()
	m.commands.logs = []commandLog{{at: time.Now(), identity: "user", command: "omo job list", output: "history output"}}
	view := m.View()
	_, ok := findRenderedCell(view, 0, "history output")
	if ok {
		t.Fatalf("history output unexpectedly rendered on row zero")
	}
	historyRow := -1
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "history output") {
			historyRow = i
			break
		}
	}
	if historyRow < 0 {
		t.Fatalf("history output is not visible:\n%s", ansi.Strip(view))
	}
	historyX, ok := findRenderedCell(view, historyRow, "history output")
	if !ok {
		t.Fatalf("history output cell is not visible:\n%s", ansi.Strip(view))
	}
	before := m.commands
	clicked := updateMouse(m, historyX, historyRow, tea.MouseButtonLeft, tea.MouseActionPress)
	if !reflect.DeepEqual(clicked.commands, before) {
		t.Fatalf("history click changed command state from %#v to %#v", before, clicked.commands)
	}

	view = m.View()
	_, ok = findRenderedCell(view, 0, "Mail: send")
	if ok {
		t.Fatalf("catalog row unexpectedly rendered on row zero")
	}
	row := -1
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, "Mail: send") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("catalog row is not visible:\n%s", ansi.Strip(view))
	}
	x, ok := findRenderedCell(view, row, "Mail: send")
	if !ok {
		t.Fatalf("catalog row cell is not visible:\n%s", ansi.Strip(view))
	}
	for _, tc := range []struct {
		name   string
		button tea.MouseButton
		action tea.MouseAction
	}{
		{name: "empty", button: tea.MouseButtonLeft, action: tea.MouseActionPress},
		{name: "right", button: tea.MouseButtonRight, action: tea.MouseActionPress},
		{name: "middle", button: tea.MouseButtonMiddle, action: tea.MouseActionPress},
		{name: "release", button: tea.MouseButtonLeft, action: tea.MouseActionRelease},
		{name: "motion", button: tea.MouseButtonLeft, action: tea.MouseActionMotion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cx, cy := x, row
			if tc.name == "empty" {
				cx, cy = 0, 0
			}
			got := updateMouse(m, cx, cy, tc.button, tc.action)
			if got.mode != modeCommandConsole || got.commands.item != m.commands.item {
				t.Fatalf("mouse event changed mode=%d item=%d", got.mode, got.commands.item)
			}
		})
	}
}
