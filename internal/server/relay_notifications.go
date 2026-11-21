package server

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/relayhost"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// Relay trust authorizes the paired human device; board notification reads use
// exactly the existing user's mention qualification, with a device-owned cursor.
type relayNotificationSource struct{ board *board.Board }

func (s relayNotificationSource) RelayMentionsAfter(ctx context.Context, after relayproto.Counter, limit, maxBytes int) (relayproto.NotificationPage, error) {
	return s.board.RelayMentionsAfter(ctx, board.User, after, limit, maxBytes)
}
func (s relayNotificationSource) RelayMentionsByID(ctx context.Context, ids []relayproto.Counter) (relayproto.NotificationPage, error) {
	return s.board.RelayMentionsByID(ctx, board.User, ids)
}
func (s *Server) pollRelayNotifications(ctx context.Context, d relayhost.Device, p relayproto.Poll) (relayproto.NotificationEnvelope, error) {
	return s.relay.PollNotifications(ctx, d, p, relayNotificationSource{board: s.board})
}

// relayMentionBaseline is where a newly paired device's cursor starts: after
// every mention that exists now.
func (s *Server) relayMentionBaseline(ctx context.Context) (relayproto.Counter, error) {
	return s.board.RelayMentionsHead(ctx, board.User)
}
