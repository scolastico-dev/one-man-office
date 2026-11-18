package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// CreateProxyForward registers a web UI that someone else starts.
func (c *Client) CreateProxyForward(ctx context.Context, req api.CreateProxyForward) (api.ProxyForward, error) {
	var out api.ProxyForward
	err := c.Do(ctx, http.MethodPost, "/api/proxy/forwards", req, &out)
	return out, err
}

// ProxyForwards lists readable forwards, optionally filtered by ticket.
func (c *Client) ProxyForwards(ctx context.Context, ticket *int) (api.ProxyForwardList, error) {
	path := "/api/proxy/forwards"
	if ticket != nil {
		path += "?ticket=" + strconv.Itoa(*ticket)
	}
	var out api.ProxyForwardList
	err := c.Do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// DeleteProxyForward removes a forward within the caller's write scope.
func (c *Client) DeleteProxyForward(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, "/api/proxy/forwards/"+url.PathEscape(id), nil, nil)
}
