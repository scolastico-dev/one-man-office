// Package prompts holds omo's default prompts and renders prompt text.
//
// Role prompts (ceo, pm, developer, reviewer, export) tell an agent what its
// role is and how the board works. Each ends with a section for the user's
// own instructions, which come from a separate prompt item
// "<role>-instructions" so that new default mechanics never overwrite what
// the user wrote. Message prompts (start, restart, handoff, wait-timeout,
// wait-investigate, stalled, question-reminder) are the short texts omo
// types to an agent or returns from a wait.
package prompts

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

//go:embed defaults/*.md
var defaultFiles embed.FS

// Names lists every prompt omo knows, in a stable order.
var Names = []string{
	"ceo", "pm", "developer", "reviewer", "export",
	"ceo-instructions", "pm-instructions", "developer-instructions", "reviewer-instructions", "export-instructions",
	"start", "restart", "handoff", "wait-timeout", "wait-investigate", "stalled", "question-reminder",
}

// Defaults returns a fresh map from every name in Names to its embedded
// default text. The "-instructions" defaults are empty.
func Defaults() map[string]string {
	out := make(map[string]string, len(Names))
	for _, name := range Names {
		data, err := defaultFiles.ReadFile("defaults/" + name + ".md")
		if err != nil {
			panic(fmt.Sprintf("prompts: missing embedded default %s: %v", name, err))
		}
		out[name] = string(data)
	}
	return out
}

// Project is one project of the ticket an agent works on.
type Project struct {
	ID       string
	Name     string
	Folder   string // the user's own checkout
	Worktree string // the agent's working folder
	Branch   string // the branch checked out in Worktree
	// Parent is the branch Branch was cut from; for a reviewer or an export
	// job, the branch it is compared with or targets.
	Parent string
}

// Data is what a prompt template can use.
type Data struct {
	Name         string // the agent's name
	Role         string
	Type         string // story or task
	Title        string
	StoryTitle   string
	Supervisor   string // who supervises the ticket, in words
	Verification string // targeted or full
	TaskRelease  string // agents or user
	WaitTimeout  string
	Silent       string
	Instructions string // the user's own instructions for the role
	RestartNote  string // the rendered restart prompt on a fresh session of started work
	Ticket       int
	Story        int
	ReviewRound  int
	Timeouts     int
	Question     int
	Projects     []Project
	MultiProject bool
	CEOEnabled   bool
	// CEOMayApprove is the setting that lets the CEO approve stories.
	CEOMayApprove bool
	// AgentReview is true when an agent reviews the story.
	AgentReview bool
	// Export is what an export job needs; empty for every other prompt.
	Export ExportData
	// StopReason is why the handoff prompt is sent: StopUsage when the
	// agent's model reached its usage limit, "" when omo is stopping.
	StopReason string
	// Home is omo's home folder and ServerLog the log file omo writes when
	// it runs in the background; both for the CEO's prompt only.
	Home      string
	ServerLog string
}

// StopUsage is the StopReason of a handoff asked because the agent's model
// reached its usage limit.
const StopUsage = "usage"

// ExportData is what omo did and found for an export job: the commit it
// pushed where, and the commands that open or update the pull request.
type ExportData struct {
	// Folder is the job's working folder: omo put material.md there, the
	// job writes title.txt and body.md, and the commands read pr-title.txt
	// and pr-body.md.
	Folder    string
	Remote    string // the name of the remote omo pushed to
	Address   string // the remote's address, without user or password
	RemoteWeb string // the repository's web address; "" for a remote on this machine
	Repo      string // the repository's path on its forge
	Forge     string // github, gitlab, gitea or ""
	CLI       string // the forge's command line tool
	Command   string // the command that opens the pull request; "" when there is none
	// EditCommand sets the title and body of the pull request that already
	// exists for the branch; for tea it names it by its number, written
	// INDEX. "" when there is no Command.
	EditCommand string
	Problem     string // why there is no command
	Commit      string // the commit omo pushed
	Branch      string // the branch omo pushed it to
	Base        string // the branch the pull request targets
}

var manyNewlines = regexp.MustCompile(`\n{3,}`)

// Render executes text as a template named name with d. An unknown field is
// an error. Trailing whitespace is removed from every line and from the end,
// and runs of three or more newlines become two.
func Render(name, text string, d Data) (string, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, d); err != nil {
		return "", err
	}
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	out := manyNewlines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimRight(out, " \t\r\n"), nil
}
