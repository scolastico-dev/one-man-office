package prompts

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/branchname"
)

// Bounds of a checked render.
const (
	// maxTemplateSize is the largest prompt text that is rendered: the
	// settings store saves no larger prompt.
	maxTemplateSize = 256 << 10
	// maxOutput is the most text one render may produce.
	maxOutput = 512 << 10
	// renderTimeout bounds one render. The checks on the template keep a
	// render far below it; it is the last line of defence.
	renderTimeout = 2 * time.Second
	// maxRangeDepth is how deep range actions may nest.
	maxRangeDepth = 2
	// maxFormatNumber is the largest width or precision printf may use.
	maxFormatNumber = 1000
	// maxMessage is the most characters of a template error that are shown.
	maxMessage = 200
)

// sampleProjects are the two projects of the sample story #7 and its task
// #12, with their worktrees where omo puts them.
func sampleProjects(ticket int, branch, parent string) []Project {
	worktree := func(id string) string {
		return "/home/you/.local/omo/worktrees/" + id + "/" + strconv.Itoa(ticket)
	}
	return []Project{
		{ID: "web", Name: "github.com/you/web", Folder: "/home/you/src/web", Worktree: worktree("web"), Branch: branch, Parent: parent},
		{ID: "api", Name: "github.com/you/api", Folder: "/home/you/src/api", Worktree: worktree("api"), Branch: branch, Parent: parent},
	}
}

// SampleData returns the data a preview renders the prompt name with. It
// has the shape the engine gives that prompt: a CEO; a product manager on
// story #7 "Sign-in"; a developer and a reviewer (review round 1) on its task
// #12 "Add the login form"; an export job for the story; two projects
// (one for the reviewer and the export job, as the engine gives them one).
// Fields the engine does not set for the prompt stay empty. An instructions
// prompt gets its role's data. Nothing in it comes from the board.
func SampleData(name string) Data {
	if role := RoleOf(name); role != "" {
		name = role
	}
	const (
		story      = 7
		storyTitle = "Sign-in"
		task       = 12
		taskTitle  = "Add the login form"
	)
	// The branch names a fresh home's template gives these tickets.
	storyBranch, _ := branchname.Expand(branchname.Default, branchname.Vars{Number: story, Title: storyTitle, Type: string(api.TypeStory)})
	taskBranch, _ := branchname.Expand(branchname.Default, branchname.Vars{Number: task, Title: taskTitle, Type: string(api.TypeTask)})
	role := func(name, role string) Data {
		return Data{Name: name, Role: role, CEOEnabled: true, TaskRelease: string(api.TaskReleaseAgents)}
	}
	switch name {
	case "ceo":
		d := role("Nora", "ceo")
		d.Home, d.ServerLog = "/home/you/.local/omo", "/home/you/.local/omo/run/omo.log"
		return d
	case "pm":
		d := role("Paul", "pm")
		d.Ticket, d.Type, d.Title = story, string(api.TypeStory), storyTitle
		d.Supervisor, d.Verification, d.AgentReview = "CEO", "full", true
		d.Projects, d.MultiProject = sampleProjects(story, storyBranch, "main"), true
		return d
	case "developer", "restart":
		d := role("Dana", "developer")
		d.Ticket, d.Type, d.Title = task, string(api.TypeTask), taskTitle
		d.Story, d.StoryTitle = story, storyTitle
		d.Supervisor, d.Verification, d.AgentReview = "product manager", "targeted", true
		d.Projects, d.MultiProject = sampleProjects(task, taskBranch, storyBranch), true
		return d
	case "reviewer":
		d := role("Rita", "reviewer")
		d.Ticket, d.Type, d.Title, d.ReviewRound = task, string(api.TypeTask), taskTitle, 1
		d.Story, d.StoryTitle = story, storyTitle
		d.Supervisor, d.Verification, d.AgentReview = "product manager", "targeted", true
		d.Projects, d.MultiProject = sampleProjects(task, taskBranch, storyBranch)[:1], true
		return d
	case "export":
		d := role("Ezra", "developer")
		d.Ticket, d.Type, d.Title = story, string(api.TypeStory), storyTitle
		d.Supervisor, d.Verification, d.AgentReview = "CEO", "full", true
		d.Projects, d.MultiProject = sampleProjects(story, storyBranch, "main")[:1], true
		d.Export = SampleExport(story, storyBranch, "main")
		return d
	case "start", "handoff":
		return Data{Name: "Dana", Role: "developer", Ticket: task}
	case "wait-timeout", "wait-investigate":
		return Data{Name: "Dana", Role: "developer", Ticket: task, WaitTimeout: "30m0s", Timeouts: 3}
	case "stalled":
		return Data{Name: "Dana", Role: "developer", Ticket: task, Silent: "15m"}
	case "question-reminder":
		return Data{Ticket: task, Question: 41}
	}
	return Data{}
}

// SampleExport is the export data of an export job of ticket in the
// sample project "web" on GitHub, pushed to branch for base.
func SampleExport(ticket int, branch, base string) ExportData {
	return ExportData{
		Folder:    "/home/you/.local/omo/run/export/" + strconv.Itoa(ticket) + "-web",
		Remote:    "origin",
		Address:   "https://github.com/you/web.git",
		RemoteWeb: "https://github.com/you/web",
		Repo:      "you/web",
		Forge:     "github",
		CLI:       "gh",
		Command: "gh pr create --repo='you/web' --head='" + branch + "' --base='" + base +
			"' --title=\"$(cat pr-title.txt)\" --body-file='pr-body.md'",
		EditCommand: "gh pr edit '" + branch + "' --repo='you/web' --title=\"$(cat pr-title.txt)\" --body-file='pr-body.md'",
		Commit:      "3f2c9a7d41e0b8c6f5a2d9e1c7b4a3f0e6d8c2b1",
		Branch:      branch,
		Base:        base,
	}
}

// Check reports whether text can be saved as the prompt name: a template
// must parse, keep within the bounds of a render (see RenderBounded) and
// render with SampleData(name). An instructions text is plain text inserted
// into its role's prompt and is only checked for its size. The error is a
// short one-line message.
func Check(name, text string) error {
	switch Kind(name) {
	case "":
		return fmt.Errorf("omo has no prompt %q", truncate(name, 64))
	case api.PromptKindInstructions:
		return checkSize(text)
	}
	_, err := RenderBounded(name, text, SampleData(name))
	return err
}

// Preview renders the draft text of the prompt name with SampleData, as an
// agent would get it. For an instructions prompt it renders roleText (the
// role's prompt in effect) with the draft as its instructions, trimmed as the
// engine trims them; roleText is unused otherwise.
func Preview(name, text, roleText string) (string, error) {
	switch Kind(name) {
	case "":
		return "", fmt.Errorf("omo has no prompt %q", truncate(name, 64))
	case api.PromptKindInstructions:
		if err := checkSize(text); err != nil {
			return "", err
		}
		role := RoleOf(name)
		d := SampleData(role)
		d.Instructions = strings.TrimSpace(text)
		out, err := RenderBounded(role, roleText, d)
		if err != nil {
			return "", fmt.Errorf("the %s prompt in effect does not render: %w", role, err)
		}
		return out, nil
	}
	return RenderBounded(name, text, SampleData(name))
}

// RenderBounded is Render with bounds: the text is at most 256 KiB; it must
// not define or call templates of its own; range may only walk .Projects
// or .Repositories,
// nested at most twice; printf needs a literal format without "*" and with
// widths and precisions of at most 1000; the output is at most 512 KiB and
// the render must finish within 2 s. Its error message is one line of at
// most 200 characters, so it never repeats much of the template.
func RenderBounded(name, text string, d Data) (string, error) {
	if err := checkSize(text); err != nil {
		return "", err
	}
	t, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", shortError(err)
	}
	if err := checkTree(t); err != nil {
		return "", err
	}
	if err := execBounded(t, d); err != nil {
		return "", err
	}
	out, err := Render(name, text, d)
	if err != nil {
		return "", shortError(err)
	}
	return out, nil
}

func checkSize(text string) error {
	if len(text) > maxTemplateSize {
		return fmt.Errorf("a prompt must be at most %d KiB", maxTemplateSize>>10)
	}
	return nil
}

// errTooLong stops a render whose output passed maxOutput.
var errTooLong = fmt.Errorf("the prompt renders to text longer than %d KiB", maxOutput>>10)

// countingWriter discards what it is given and fails once more than
// maxOutput bytes arrived.
type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	if w.n > maxOutput {
		return 0, errTooLong
	}
	return len(p), nil
}

// execBounded executes t once into a counting writer, within renderTimeout.
// A render that does not finish in time is left to finish on its own; the
// checks of checkTree keep that from running long.
func execBounded(t *template.Template, d Data) error {
	done := make(chan error, 1)
	go func() { done <- t.Execute(&countingWriter{}, d) }()
	timer := time.NewTimer(renderTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if errors.Is(err, errTooLong) {
			return errTooLong
		}
		if err != nil {
			return shortError(err)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("the prompt did not render within %s", renderTimeout)
	}
}

// checkTree refuses templates whose render could run long or grow large
// without writing much: own templates (define, block, template), range over
// anything but .Projects or .Repositories, deeply nested ranges and printf with a computed
// format or a huge width.
func checkTree(t *template.Template) error {
	if len(t.Templates()) > 1 {
		return errors.New("a prompt cannot define templates of its own ({{define}} or {{block}})")
	}
	if t.Tree == nil || t.Tree.Root == nil {
		return nil
	}
	return checkNode(t.Tree.Root, 0)
}

func checkNode(n parse.Node, depth int) error {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, c := range n.Nodes {
			if err := checkNode(c, depth); err != nil {
				return err
			}
		}
	case *parse.ActionNode:
		return checkPipe(n.Pipe)
	case *parse.IfNode:
		return checkBranch(&n.BranchNode, depth, depth)
	case *parse.WithNode:
		return checkBranch(&n.BranchNode, depth, depth)
	case *parse.RangeNode:
		if !walksProjects(n.Pipe) {
			return errors.New("range may only walk .Projects or .Repositories")
		}
		if depth+1 > maxRangeDepth {
			return fmt.Errorf("range may be nested at most %d deep", maxRangeDepth)
		}
		return checkBranch(&n.BranchNode, depth+1, depth)
	case *parse.TemplateNode:
		return errors.New("a prompt cannot call templates ({{template}})")
	}
	return nil
}

func checkBranch(b *parse.BranchNode, listDepth, elseDepth int) error {
	if err := checkPipe(b.Pipe); err != nil {
		return err
	}
	if err := checkNode(b.List, listDepth); err != nil {
		return err
	}
	if b.ElseList != nil {
		return checkNode(b.ElseList, elseDepth)
	}
	return nil
}

func checkPipe(p *parse.PipeNode) error {
	if p == nil {
		return nil
	}
	for _, cmd := range p.Cmds {
		if len(cmd.Args) > 0 {
			if id, ok := cmd.Args[0].(*parse.IdentifierNode); ok && id.Ident == "printf" {
				if err := checkPrintf(cmd); err != nil {
					return err
				}
			}
		}
		for _, arg := range cmd.Args {
			var err error
			switch a := arg.(type) {
			case *parse.PipeNode:
				err = checkPipe(a)
			case *parse.ChainNode:
				if inner, ok := a.Node.(*parse.PipeNode); ok {
					err = checkPipe(inner)
				}
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// walksProjects reports whether a range pipeline is .Projects or
// .Repositories (or the same on $), with no further command.
func walksProjects(p *parse.PipeNode) bool {
	if p == nil || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	lists := func(name string) bool { return name == "Projects" || name == "Repositories" }
	switch a := p.Cmds[0].Args[0].(type) {
	case *parse.FieldNode:
		return len(a.Ident) == 1 && lists(a.Ident[0])
	case *parse.VariableNode:
		return len(a.Ident) == 2 && a.Ident[0] == "$" && lists(a.Ident[1])
	}
	return false
}

func checkPrintf(cmd *parse.CommandNode) error {
	if len(cmd.Args) < 2 {
		return nil
	}
	format, ok := cmd.Args[1].(*parse.StringNode)
	if !ok {
		return errors.New(`printf needs a format written in the prompt, such as printf "%s" .Title`)
	}
	f := format.Text
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			continue
		}
		for i++; i < len(f); i++ {
			c := f[i]
			if c == '*' {
				return errors.New(`printf formats may not take a width or precision from an argument ("*")`)
			}
			if c >= '0' && c <= '9' {
				j := i
				for j < len(f) && f[j] >= '0' && f[j] <= '9' {
					j++
				}
				if n, err := strconv.Atoi(f[i:j]); err != nil || n > maxFormatNumber {
					return fmt.Errorf("printf widths and precisions may be at most %d", maxFormatNumber)
				}
				i = j - 1
				continue
			}
			if strings.IndexByte("+-# .[]", c) < 0 {
				break // the verb ends the directive
			}
		}
	}
	return nil
}

var (
	// templateWhere is the prefix Go puts on a template error: the
	// template's name, line and column, and for an execution error the
	// command it stopped at.
	templateWhere = regexp.MustCompile(`^template: [^:]*:(\d+)(?::\d+)?: (?:executing "[^"]*" at <.*?>: )?`)
	// templateQuoted is a piece of the template Go quotes in the rest of
	// the message: a quoted token or a command in angle brackets.
	templateQuoted = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|<[^<>]*>`)
	// templateName is a quoted token that is a name (a function, a field),
	// which the message keeps: it says what to fix.
	templateName = regexp.MustCompile(`^"[A-Za-z_][A-Za-z0-9_]{0,40}"$`)
)

// shortError turns a template error into one line of at most maxMessage
// characters that names the line and quotes no text of the template beyond
// the name of a function or field: a prompt can hold anything, and the
// message goes into events and answers.
func shortError(err error) error {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	line := ""
	if m := templateWhere.FindStringSubmatchIndex(msg); m != nil {
		line = msg[m[2]:m[3]]
		msg = msg[m[1]:]
	}
	msg = templateQuoted.ReplaceAllStringFunc(msg, func(q string) string {
		if templateName.MatchString(q) {
			return q
		}
		return "…"
	})
	if line != "" {
		msg = "line " + line + ": " + msg
	}
	return errors.New(truncate(msg, maxMessage))
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
