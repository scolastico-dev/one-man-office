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

func lspCommands() []Command {
	return []Command{
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
