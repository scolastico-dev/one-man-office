package tui

import (
	"fmt"
	"strings"
)

type commandInput struct {
	Name        string
	Flag        string
	Help        string
	Required    bool
	Positional  bool
	Boolean     bool
	Suggestions []string
}

type commandSpec struct {
	Title       string
	Path        string
	Help        string
	Inputs      []commandInput
	Destructive bool
	Raw         bool
}

func input(name, flag, help string, required bool, suggestions ...string) commandInput {
	return commandInput{Name: name, Flag: flag, Help: help, Required: required, Suggestions: suggestions}
}

func positional(name, help string, required bool, suggestions ...string) commandInput {
	return commandInput{Name: name, Help: help, Required: required, Positional: true, Suggestions: suggestions}
}

func boolean(name, flag, help string) commandInput {
	return commandInput{Name: name, Flag: flag, Help: help, Boolean: true, Suggestions: []string{"false", "true"}}
}

func commandCatalog() []commandSpec {
	roles := []string{"product_manager", "developer", "freelancer"}
	priorities := []string{"normal", "high", "urgent", "low"}
	return []commandSpec{
		{Title: "Mail: send", Path: "send", Help: "Send a durable message as the selected identity.", Inputs: []commandInput{
			positional("body", "Message body; use --body-file in Advanced mode for files.", true),
			input("to", "to", "Agent name, role, user, or blank for broadcast.", false),
			input("subject", "subject", "Short subject line.", true),
			input("priority", "priority", "Delivery priority.", false, priorities...),
		}},
		{Title: "Mail: inbox", Path: "inbox", Help: "List messages visible to the selected identity."},
		{Title: "Mail: read", Path: "read", Help: "Read and acknowledge one message.", Inputs: []commandInput{positional("message id", "Numeric message ID.", true)}},
		{Title: "Agent: ready", Path: "ready", Help: "Tell omo this agent finished startup."},
		{Title: "Agent: done", Path: "done", Help: "Finish the selected agent's job.", Inputs: []commandInput{positional("result", "Optional completion result.", false)}},
		{Title: "Agent: wait", Path: "wait", Help: "Wait for messages or shutdown instructions.", Inputs: []commandInput{input("timeout", "timeout", "Duration such as 30s or 5m; blank waits indefinitely.", false)}},
		{Title: "Agent: step", Path: "step", Help: "Publish the selected agent's current status.", Inputs: []commandInput{positional("status", "Current work status.", true)}},
		{Title: "Job: create", Path: "job create", Help: "Create a queued office job.", Inputs: []commandInput{
			input("title", "title", "Short job title.", true), input("goal", "goal", "Full job goal.", true),
			input("role", "role", "Role that should execute it.", true, roles...), input("model", "model", "Optional selectable model profile override.", false),
			input("repo", "repo", "Configured repository key; required for developers.", false), input("parent", "parent", "Optional parent job ID.", false),
		}},
		{Title: "Job: list", Path: "job list", Help: "List jobs visible to the selected identity."},
		{Title: "Job: show", Path: "job show", Help: "Show complete job details.", Inputs: []commandInput{positional("job id", "Numeric job ID.", true)}},
		{Title: "Job: verdict", Path: "job verdict", Help: "Reviewer merge/reject verdict.", Inputs: []commandInput{
			positional("job id", "Numeric job ID.", true), positional("verdict", "Merge or reject.", true, "merge", "reject"), input("notes", "notes", "Required findings for a rejection.", false),
		}},
		{Title: "Job: override", Path: "job override", Help: "Override a review rejection.", Destructive: true, Inputs: []commandInput{
			positional("job id", "Numeric job ID.", true), input("notes", "notes", "Why the rejection is out of scope.", true),
		}},
		{Title: "Job: cancel", Path: "job cancel", Help: "Cancel a queued or active job.", Destructive: true, Inputs: []commandInput{positional("job id", "Numeric job ID.", true)}},
		{Title: "Job: requeue", Path: "job requeue", Help: "Put a failed or cancelled job back in the queue.", Inputs: []commandInput{positional("job id", "Numeric job ID.", true)}},
		{Title: "Agent: list", Path: "agent list", Help: "List office agents."},
		{Title: "Agent: kill", Path: "agent kill", Help: "Terminate an agent or role.", Destructive: true, Inputs: []commandInput{positional("name or role", "Agent name or role.", true)}},
		{Title: "Agent: restart", Path: "agent restart", Help: "Restart an agent or role.", Destructive: true, Inputs: []commandInput{positional("name or role", "Agent name or role.", true)}},
		{Title: "Incident: create", Path: "incident create", Help: "Open an incident.", Inputs: []commandInput{
			input("agent", "agent", "Affected agent name.", true), input("class", "class", "Incident class.", true, "stuck", "looping", "drifting", "too-slow", "other"), input("detail", "detail", "Evidence.", false),
		}},
		{Title: "Incident: resolve", Path: "incident resolve", Help: "Resolve an incident and report it.", Inputs: []commandInput{
			positional("incident id", "Numeric incident ID.", true), input("report", "report", "Resolution report sent to the user.", true),
		}},
		{Title: "Office: pause", Path: "office pause", Help: "Pause spawning new agents."},
		{Title: "Office: resume", Path: "office resume", Help: "Resume spawning."},
		{Title: "Office: halt spawns", Path: "office halt-spawns", Help: "CEO: halt work-agent spawning."},
		{Title: "Office: resume spawns", Path: "office resume-spawns", Help: "CEO: resume work-agent spawning."},
		{Title: "Office: safe shutdown", Path: "safe-shutdown", Help: "Ask agents to save context and stop safely.", Destructive: true},
		{Title: "Office: emergency stop", Path: "estop", Help: "Immediately terminate the running office.", Destructive: true},
		{Title: "Context: save", Path: "context save", Help: "Save durable handoff context.", Inputs: []commandInput{positional("summary", "Handoff summary.", true)}},
		{Title: "Logs: tail", Path: "logs", Help: "Read an agent transcript tail.", Inputs: []commandInput{
			positional("agent", "Agent name.", true), input("lines", "lines", "Number of trailing lines (1-10000).", false),
		}},
		{Title: "Input: type", Path: "type", Help: "Send terminal input to an agent.", Inputs: []commandInput{
			positional("agent", "Agent name.", true), positional("text", "Text to type.", false), input("key", "key", "Optional named key; comma-separated values belong in Advanced mode.", false),
		}},
		{Title: "Repository: list", Path: "repo list", Help: "List configured repositories."},
		{Title: "Repository: add", Path: "repo add", Help: "Add or update a configured repository.", Inputs: []commandInput{
			positional("name", "Repository key.", true), positional("path", "Repository filesystem path.", true),
		}},
		{Title: "Repository: remove", Path: "repo remove", Help: "Remove a repository from configuration.", Destructive: true, Inputs: []commandInput{positional("name", "Repository key.", true)}},
		{Title: "Configuration: reload", Path: "reload", Help: "Reload omo.yaml into the running office."},
		{Title: "Advanced: raw command", Help: "Enter any omo subcommand exactly as text.", Raw: true},
	}
}

func buildGuidedCommand(spec commandSpec, values []string) (string, error) {
	if spec.Raw {
		return "", fmt.Errorf("raw command has no guided builder")
	}
	parts := append([]string{"omo"}, strings.Fields(spec.Path)...)
	for i, field := range spec.Inputs {
		value := ""
		if i < len(values) {
			value = strings.TrimSpace(values[i])
		}
		if field.Required && value == "" {
			return "", fmt.Errorf("%s is required", field.Name)
		}
		if value == "" || (field.Boolean && !strings.EqualFold(value, "true") && !strings.EqualFold(value, "yes")) {
			continue
		}
		if !field.Positional {
			parts = append(parts, "--"+field.Flag)
		}
		if !field.Boolean {
			parts = append(parts, quoteCommandArg(value))
		}
	}
	return strings.Join(parts, " "), nil
}

func quoteCommandArg(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n'\"\\") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
