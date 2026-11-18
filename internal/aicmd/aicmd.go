// Package aicmd is the one definition of every `omo ai` command: its path,
// help, input, who may use it as an MCP tool and what it does. The command
// line (internal/cli) and the MCP endpoint are both built from Commands, and
// both run a command through the same Run over an apiclient.Client, so they
// go through the same routes, rules and refusals.
//
// An input is a struct. Its fields carry these tags:
//
//	json, jsonschema   the MCP tool's argument name and description
//	arg:"N"            the command line's positional argument N (an int is a
//	                   ticket number, written 12 or #12); "N,optional" may be
//	                   left out; "N..." takes the words from N on (text only)
//	flag:"name"        the command line flag --name; help:"…" is its usage,
//	                   default:"…" its default and required:"true" makes it
//	                   required; a pointer field is set only when the flag is
//	                   given
//	text:"file-flag"   a text: on the command line the words of its arg tag,
//	                   or --file-flag FILE, or standard input (a lone - or
//	                   --file-flag -); ",empty" allows an empty text; a
//	                   pointer field is nil when no text was given
//	stdin:"hint"       the flag's value - reads standard input; hint says
//	                   how to pass the value when that fails
//
// On MCP every field is a plain value: no file and no standard input.
package aicmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

// Result is what a command answers: Text is the command line's text form,
// Value the route's JSON answer (what --json prints).
type Result struct {
	Text  string
	Value json.RawMessage
}

// Audience is an agent session as the MCP endpoint sees it: an export job
// has the role developer and the purpose export.
type Audience struct {
	Role    api.Role
	Purpose api.Purpose
}

// Command is one `omo ai` command.
type Command struct {
	// Path is the command's words after `omo ai`.
	Path []string
	// Tool is the MCP tool name: the path joined with _, - written _.
	Tool string
	// Use, Short, Long and Example are the command line's help.
	Use, Short, Long, Example string
	// CommandLineOnly commands are no MCP tool: they block for a long time
	// or belong to the user. Sees is nil for them.
	CommandLineOnly bool
	// Sees reports whether an agent session sees the command as an MCP
	// tool. It is coarse; the route decides in detail.
	Sees func(Audience) bool
	// Input is the input struct (see the package documentation).
	Input reflect.Type
	// Run runs the command with in, an Input value or a pointer to one.
	Run func(ctx context.Context, c *apiclient.Client, in any) (Result, error)
}

// checker is an input with checks that come before any text is read: the
// command line runs them before it reads a file or standard input.
type checker interface {
	check() error
}

// Def defines a command; path is its words after `omo ai`, separated by
// spaces.
func Def[In any](path, short string, sees func(Audience) bool, run func(context.Context, *apiclient.Client, In) (Result, error)) Command {
	words := strings.Fields(path)
	return Command{
		Path:  words,
		Tool:  strings.ReplaceAll(strings.Join(words, "_"), "-", "_"),
		Short: short,
		Sees:  sees,
		Input: reflect.TypeFor[In](),
		Run: func(ctx context.Context, c *apiclient.Client, in any) (Result, error) {
			v, err := inputOf[In](in)
			if err != nil {
				return Result{}, err
			}
			if ch, ok := any(v).(checker); ok {
				if err := ch.check(); err != nil {
					return Result{}, err
				}
			}
			return run(ctx, c, v)
		},
	}
}

func inputOf[In any](in any) (In, error) {
	switch v := in.(type) {
	case In:
		return v, nil
	case *In:
		return *v, nil
	}
	var zero In
	return zero, fmt.Errorf("input %T is not %T", in, zero)
}

// Check runs the checks of in (an Input value or pointer) that come before
// any text is read; the command line calls it with the texts marked as
// given but not read yet.
func (c Command) Check(in any) error {
	v := reflect.ValueOf(in)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if ch, ok := v.Interface().(checker); ok {
		return ch.check()
	}
	return nil
}

// help sets the command line's help.
func (c Command) help(use, long, example string) Command {
	c.Use, c.Long, c.Example = use, long, example
	return c
}

// commandLineOnly marks c as no MCP tool.
func (c Command) commandLineOnly() Command {
	c.CommandLineOnly, c.Sees = true, nil
	return c
}

// Group is a command group: `omo ai` itself (Path empty) or a word under it
// that holds commands.
type Group struct {
	Path                      []string
	Use, Short, Long, Example string
}

// Explain reduces an error to the server's message plus its code; any other
// error is returned as it is.
func Explain(err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%s (%s)", apiErr.Message, apiErr.Code)
	}
	return err
}

// Who sees a tool.

func everyone(Audience) bool { return true }

// agentSession is every agent session.
func agentSession(a Audience) bool {
	switch a.Role {
	case api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer:
		return true
	}
	return false
}

// handsIn is the developer, product manager, reviewer and export job.
func handsIn(a Audience) bool {
	switch a.Role {
	case api.RolePM, api.RoleDeveloper, api.RoleReviewer:
		return true
	}
	return false
}

func exportJob(a Audience) bool { return a.Purpose == api.PurposeExport }

// Commands returns every `omo ai` command.
func Commands() []Command {
	var out []Command
	out = append(out, ticketCommands()...)
	out = append(out, commentCommands()...)
	out = append(out, miscCommands()...)
	out = append(out, imageCommands()...)
	out = append(out, proxyCommands()...)
	return out
}

// Groups returns `omo ai` and its command groups.
func Groups() []Group {
	return []Group{
		{Use: "ai", Short: "Commands for agents working on the board", Long: aiLong, Example: aiExample},
		{Path: []string{"ticket"}, Use: "ticket", Short: "Read, create, update and move tickets", Example: "  omo ai ticket show 12"},
		{Path: []string{"project"}, Use: "project", Short: "List and search the registered projects", Example: "  omo ai project list"},
		{Path: []string{"model"}, Use: "model", Short: "List the models you may choose for tickets", Example: "  omo ai model list"},
		{Path: []string{"image"}, Use: "image", Short: "Images on tickets: add one, then write its reference into a comment or description", Example: "  omo ai image add 12 ./screenshot.png"},
		{Path: []string{"proxy"}, Use: "proxy", Short: "Register, list and remove web UI forwards", Example: "  omo ai proxy add --port 5173 --name \"dev server\""},
	}
}

const aiLong = `The commands agents use to work on the omo board: read tickets, create and
update them, link dependencies, comment, ask questions and hand in work. A
project that already has a branch on a ticket is taken off it with
omo ai ticket remove-project.
Every command talks to the running omo server over its local socket and never
starts one. Without OMO_AGENT_ID you act as the user; with it, as that agent.

Ticket numbers are written 12 or #12. Text arguments (descriptions, comments)
can be given as words, from a file, or from standard input: a lone "-" in
place of the words, or the file flag with "-" (for example --body-file -).
Text must be UTF-8; line endings become \n and whitespace at the very end is
dropped, everything else is sent as it is.
Add --json to any command for the server's raw JSON answer. An error prints
one line and exits with status 1.

Text written by people or agents (descriptions, comments) is indented by four
spaces; lines at the left margin come from omo. An image in a description or
comment is written ![name](omo-image:<id>); omo follows it with the local
path of the image's file, which you can open.`

const aiExample = `  omo ai whoami
  omo ai ticket list --column ready
  omo ai ticket show 12
  omo ai comments 12
  omo ai comment 12 "@developer please check the failing test"
  omo ai image add 12 ./screenshot.png`

// textOf runs print into a string.
func textOf(print func(w *strings.Builder)) string {
	var b strings.Builder
	print(&b)
	return b.String()
}

// last is the result of a call whose answer is the route's last body.
func last(c *apiclient.Client, text string) Result {
	return Result{Text: text, Value: json.RawMessage(c.LastBody())}
}
