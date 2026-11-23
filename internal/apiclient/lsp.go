package apiclient

import (
	"context"
	"net/http"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// LSPIgnore suppresses a language-server issue in the caller's worktrees.
func (c *Client) LSPIgnore(ctx context.Context, id string) (api.LSPIgnoreResult, error) {
	var out api.LSPIgnoreResult
	err := c.Do(ctx, http.MethodPost, "/api/lsp/ignore", api.LSPIgnoreRequest{ID: id}, &out)
	return out, err
}

// LSPConfirm returns the token of a notification probe omo sent over the
// caller's MCP stream, which confirms that the caller sees the notifications.
func (c *Client) LSPConfirm(ctx context.Context, token string) (api.LSPConfirmResult, error) {
	var out api.LSPConfirmResult
	err := c.Do(ctx, http.MethodPost, "/api/lsp/confirm", api.LSPConfirmRequest{Token: token}, &out)
	return out, err
}
