package apiclient

import (
	"context"
	"net/http"
	"net/url"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func (c *Client) RelayStatus(ctx context.Context) (api.RelayStatus, error) {
	var out api.RelayStatus
	e := c.Do(ctx, http.MethodGet, "/api/relay/status", nil, &out)
	return out, e
}
func (c *Client) RelayConnect(ctx context.Context, req api.RelayConnectRequest) (api.RelayStatus, error) {
	var out api.RelayStatus
	e := c.Do(ctx, http.MethodPost, "/api/relay/connect", req, &out)
	return out, e
}
func (c *Client) RelayDisconnect(ctx context.Context) error {
	return c.Do(ctx, http.MethodPost, "/api/relay/disconnect", struct{}{}, nil)
}
func (c *Client) RelayPairBegin(ctx context.Context) error {
	return c.Do(ctx, http.MethodPost, "/api/relay/pair/begin", struct{}{}, nil)
}
func (c *Client) RelayPairPending(ctx context.Context) (api.RelayPending, error) {
	var out api.RelayPending
	e := c.Do(ctx, http.MethodGet, "/api/relay/pair/pending", nil, &out)
	return out, e
}
func (c *Client) RelayPairAccept(ctx context.Context, req api.RelayAcceptRequest) (api.RelayAccepted, error) {
	var out api.RelayAccepted
	e := c.Do(ctx, http.MethodPost, "/api/relay/pair/accept", req, &out)
	return out, e
}
func (c *Client) RelayPairCancel(ctx context.Context) error {
	return c.Do(ctx, http.MethodPost, "/api/relay/pair/cancel", struct{}{}, nil)
}
func (c *Client) RelayDevices(ctx context.Context) (api.RelayDeviceList, error) {
	var out api.RelayDeviceList
	e := c.Do(ctx, http.MethodGet, "/api/relay/devices", nil, &out)
	return out, e
}
func (c *Client) RelayUnregister(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, "/api/relay/devices/"+url.PathEscape(id), nil, nil)
}
