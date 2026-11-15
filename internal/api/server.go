package api

// PausedRequest is the body of PUT /api/server/paused. The key is required.
type PausedRequest struct {
	Paused bool `json:"paused"`
}
