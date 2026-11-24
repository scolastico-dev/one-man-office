package aicmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

type lspIgnoreIn struct {
	ID string `json:"id" arg:"0" jsonschema:"the id of the language-server issue, as the notification names it"`
}

type lspConfirmIn struct {
	Token string `json:"token" arg:"0" jsonschema:"the token in omo's notification probe"`
}

func lspCommands() []Command {
	return []Command{
		Def("lsp confirm", "Confirm that you receive omo's MCP notifications, using the token of omo's probe", agentSession, lspConfirm).help("confirm TOKEN",
			`When an MCP notification stream of yours opens, omo sends a probe over
it, a notification that contains a token. If you can read that notification,
run this command with the token (or call the MCP tool lsp_confirm). omo then
tells you about language-server issues over MCP instead of typing them into
the terminal. Without the confirmation omo keeps using the terminal. A new
stream (a reconnect, a restart of omo) sends a new probe. A token that does
not belong to a stream of your own session is refused.`,
			"  omo ai lsp confirm 3f9a1c0be2d84a6f"),
		Def("lsp ignore", "Stop being told about a wrong language-server issue in your worktree", handsIn, lspIgnore).help("ignore ID",
			`Silence a language-server issue that omo reported in your worktree and
that is wrong or not yours to fix. ID is the id in the notification, written
"(id ...)" after the issue. The issue is no longer reported to you, not in
reports already waiting and not when it comes up again; the language server
keeps reporting it, and other issues are reported as before. Nothing changes
for other worktrees. An id that names no issue of your worktree is refused.`,
			"  omo ai lsp ignore 3f9a1c0be2d84a6f"),
	}
}

func lspIgnore(ctx context.Context, c *apiclient.Client, in lspIgnoreIn) (Result, error) {
	res, err := c.LSPIgnore(ctx, strings.TrimSpace(in.ID))
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		fmt.Fprintf(w, "issue %s ignored: %s:%d:%d in %s\n", OneLine(res.ID), OneLine(res.File), res.Line, res.Column, OneLine(res.Worktree))
		fmt.Fprintf(w, "%s\n", OneLine(res.Message))
		fmt.Fprintln(w, "It is no longer reported to you in this worktree, queued or in the future; other issues still are.")
	})), nil
}

func lspConfirm(ctx context.Context, c *apiclient.Client, in lspConfirmIn) (Result, error) {
	_, err := c.LSPConfirm(ctx, strings.TrimSpace(in.Token))
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		fmt.Fprintln(w, "confirmed: omo now tells you about language-server issues over MCP while this stream stays open.")
	})), nil
}
