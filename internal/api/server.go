package api

// PausedRequest is the body of PUT /api/server/paused. The key is required: a
// pointer tells an absent key from false.
type PausedRequest struct {
	Paused *bool `json:"paused"`
}
