package api

// UI protocol features are additive; clients ignore flags they do not know.
const (
	FeatureUIForwardTunnel       = "ui.forwards.tunnel.v1"
	FeatureUICapabilities        = "ui.capabilities.v1"
	FeatureUINotificationPolling = "ui.notifications.poll.v1"
)

// UICapabilities is the request and response of POST /api/ui/capabilities.
// Each side lists its own features, without requiring exact version parity.
type UICapabilities struct {
	Features []string `json:"features"`
}

// UINotification is a system notification derived from a committed event.
// Destination is a board-relative path, never an external address.
type UINotification struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // mention, question, attention or review
	Title       string `json:"title"`
	Body        string `json:"body"`
	Destination string `json:"destination"`
}

// UINotificationFeed is the answer of GET /api/ui/notifications.
// Cursors are decimal strings to preserve 64-bit event IDs in every client.
type UINotificationFeed struct {
	Notifications []UINotification `json:"notifications"`
	NextCursor    string           `json:"next_cursor"`
	// HasMore refers to unscanned events; a page may contain no notifications.
	HasMore bool `json:"has_more"`
}

// UINotificationCursorReset accompanies a conflict for unavailable history.
// ResetCursor replays retained events; LatestCursor starts after all history.
type UINotificationCursorReset struct {
	ResetCursor  string `json:"reset_cursor"`
	LatestCursor string `json:"latest_cursor"`
}
