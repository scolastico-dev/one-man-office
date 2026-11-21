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
