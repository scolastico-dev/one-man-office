package relayhost

import (
	"context"
	"crypto/rand"
	"slices"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// NotificationSource reads bounded current previews, never cached content.
// After and ByID must each use a consistent source snapshot. Returned pages
// have no receipt fields; ByID preserves every requested ID, including deleted
// sources as unavailable entries.
type NotificationSource interface {
	RelayMentionsAfter(context.Context, relayproto.Counter, int, int) (relayproto.NotificationPage, error)
	RelayMentionsByID(context.Context, []relayproto.Counter) (relayproto.NotificationPage, error)
}

// PollNotifications implements the notification consumer. Install it through
// Callbacks.PollNotifications: the manager already holds the per-device gate
// and joins cancellation/revocation through the final network write. It must
// not be wrapped in a second WithDevice call (which would acquire that gate twice).
func (m *Manager) PollNotifications(ctx context.Context, snapshot Device, poll relayproto.Poll, source NotificationSource) (relayproto.NotificationEnvelope, error) {
	d, nc, err := m.notificationDevice(ctx, snapshot)
	if err != nil {
		return relayproto.NotificationEnvelope{}, err
	}
	request, err := relayproto.VerifyPoll(nc, d.NotificationRoot, poll)
	if err != nil {
		return relayproto.NotificationEnvelope{}, err
	}
	var acknowledged relayproto.Bytes
	if request.Ack != nil {
		switch {
		case d.Pending != nil && request.Ack.Equal(d.Pending.Receipt):
			receipt := cloneReceipt(&d.Pending.Receipt)
			// Cursor and exact last_ack commit together before reading the next page.
			if err = m.UpdateDevice(d, func(next *Device) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				next.Cursor = receipt.Through
				next.LastAck = receipt
				next.Pending = nil
				return nil
			}); err != nil {
				return relayproto.NotificationEnvelope{}, err
			}
			d.Cursor = receipt.Through
			d.LastAck = receipt
			d.Pending = nil
			acknowledged = slices.Clone(receipt.Batch)
		case d.LastAck != nil && request.Ack.Equal(*d.LastAck):
			acknowledged = slices.Clone(d.LastAck.Batch)
		default:
			return m.sealNotification(ctx, d, nc, request.Request, relayproto.NotificationPage{Error: "invalid_ack"})
		}
	}
	var page relayproto.NotificationPage
	if d.Pending != nil {
		page, err = source.RelayMentionsByID(ctx, slices.Clone(d.Pending.IDs))
		if err != nil {
			return relayproto.NotificationEnvelope{}, err
		}
		if len(page.Notifications) != len(d.Pending.IDs) {
			return relayproto.NotificationEnvelope{}, relayproto.ErrProtocol
		}
		for i, n := range page.Notifications {
			if n.ID != d.Pending.IDs[i] {
				return relayproto.NotificationEnvelope{}, relayproto.ErrProtocol
			}
		}
		page.Batch = slices.Clone(d.Pending.Receipt.Batch)
		page.Token = slices.Clone(d.Pending.Receipt.Token)
		page.Through = d.Pending.Receipt.Through
	} else {
		page, err = source.RelayMentionsAfter(ctx, d.Cursor, 50, relayproto.MaxNotificationPlaintext)
		if err != nil {
			return relayproto.NotificationEnvelope{}, err
		}
		if len(page.Notifications) > 0 {
			if page.Notifications[0].ID <= d.Cursor {
				return relayproto.NotificationEnvelope{}, relayproto.ErrProtocol
			}
			page.Batch = make([]byte, 16)
			page.Token = make([]byte, 32)
			if _, err = rand.Read(page.Batch); err != nil {
				return relayproto.NotificationEnvelope{}, err
			}
			if _, err = rand.Read(page.Token); err != nil {
				return relayproto.NotificationEnvelope{}, err
			}
			page.Acknowledged = acknowledged
			// Validate bounds/order before committing receipt metadata.
			if _, err = relayproto.EncodeNotificationPage(page); err != nil {
				return relayproto.NotificationEnvelope{}, err
			}
			pending := &PendingReceipt{Receipt: relayproto.Receipt{Batch: page.Batch, Through: page.Through, Token: page.Token}, IDs: make([]relayproto.Counter, len(page.Notifications))}
			for i, n := range page.Notifications {
				pending.IDs[i] = n.ID
			}
			if _, _, err = m.notificationDevice(ctx, d); err != nil {
				return relayproto.NotificationEnvelope{}, err
			}
			if err = m.UpdateDevice(d, func(next *Device) error {
				if e := ctx.Err(); e != nil {
					return e
				}
				next.Pending = pending
				return nil
			}); err != nil {
				return relayproto.NotificationEnvelope{}, err
			}
		} else {
			page = relayproto.NotificationPage{Through: d.Cursor, Notifications: []relayproto.Notification{}}
		}
	}
	page.Acknowledged = acknowledged
	return m.sealNotification(ctx, d, nc, request.Request, page)
}

// Source reads and encryption never hold the global store/manager lock.
func (m *Manager) notificationDevice(ctx context.Context, snapshot Device) (Device, relayproto.NotificationContext, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return Device{}, relayproto.NotificationContext{}, ctx.Err()
	}
	if !m.enabled || m.closed || m.ctx.Err() != nil {
		return Device{}, relayproto.NotificationContext{}, ErrDenied
	}
	d, err := m.findDeviceLocked(snapshot.UIID, snapshot.PairEpoch, true)
	if err != nil || d.Generation != snapshot.Generation {
		return Device{}, relayproto.NotificationContext{}, ErrDenied
	}
	v, err := m.store.Snapshot()
	if err != nil {
		return Device{}, relayproto.NotificationContext{}, err
	}
	nc := relayproto.NotificationContext{Route: relayproto.Route{Origin: v.Origin, HostID: v.HostID}, HostFP: v.HostFP, UIID: d.UIID, PairEpoch: slices.Clone(d.PairEpoch)}
	return d, nc, nil
}
func (m *Manager) sealNotification(ctx context.Context, d Device, nc relayproto.NotificationContext, request []byte, page relayproto.NotificationPage) (relayproto.NotificationEnvelope, error) {
	if _, _, err := m.notificationDevice(ctx, d); err != nil {
		return relayproto.NotificationEnvelope{}, err
	}
	env, err := relayproto.SealNotification(nc, d.NotificationRoot, request, page, nil)
	if err != nil {
		return relayproto.NotificationEnvelope{}, err
	}
	if _, _, err = m.notificationDevice(ctx, d); err != nil {
		return relayproto.NotificationEnvelope{}, err
	}
	return env, nil
}
