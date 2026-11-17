package apiclient

import (
	"context"
	"io"
	"net/http"
)

// Raw sends a request whose body (may be nil) is raw bytes of contentType
// and returns the response for the caller to read and close: for answers
// that are no JSON (a download) or bodies that are none (an upload). A
// non-2xx answer is returned as *api.Error, and the response is closed.
func (c *Client) Raw(ctx context.Context, method, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://omo"+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.session != "" {
		req.Header.Set(SessionHeader, c.session)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrNotRunning
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
		return nil, decodeError(resp.StatusCode, data)
	}
	return resp, nil
}
