package board

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/images"
)

// imageRule says who may add and delete images.
const imageRule = "whoever may comment on a ticket may add images to it (" + commentRule + "); only the user deletes images"

// NewImage is an image whose file has been stored, to be recorded by
// AddImage: its id (the file's UUID), the cleaned original name, the sniffed
// content type and the size in bytes.
type NewImage struct {
	ID          string
	Name        string
	ContentType string
	Size        int64
}

const imageSelect = `SELECT i.id, i.ticket_id, i.author_role, COALESCE(i.author_session, ''), COALESCE(s.name, ''),
	i.name, i.content_type, i.size, i.created_at, i.expired_at
	FROM images i LEFT JOIN agent_sessions s ON s.id = i.author_session`

func scanImage(row interface{ Scan(...any) error }) (api.Image, error) {
	var im api.Image
	var created string
	var expired sql.NullString
	if err := row.Scan(&im.ID, &im.Ticket, &im.Author.Role, &im.Author.Session, &im.Author.Name,
		&im.Name, &im.ContentType, &im.Size, &created, &expired); err != nil {
		return im, err
	}
	var err error
	if im.CreatedAt, err = parseTime(created); err != nil {
		return im, err
	}
	if im.ExpiredAt, err = parseNullTime(expired); err != nil {
		return im, err
	}
	im.State = api.ImageStored
	if im.ExpiredAt != nil {
		im.State = api.ImageExpired
	}
	im.Markdown = images.Markdown(im.Name, im.ID)
	return im, nil
}

func loadImage(q db.Queryer, id string) (api.Image, error) {
	im, err := scanImage(q.QueryRow(imageSelect+` WHERE i.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return im, api.Errorf(api.CodeNotFound, "image %s does not exist", id)
	}
	return im, err
}

// mayAddImage returns nil when a may add an image of size bytes to ticket n
// now: a reads and may comment on n, n exists, and neither the ticket's nor
// the overall limit would be passed.
func mayAddImage(q db.Queryer, a Actor, n int64, size int64) error {
	if err := checkRead(q, a, n); err != nil {
		return err
	}
	if err := canComment(q, a, n); err != nil {
		var e *api.Error
		if errors.As(err, &e) && e.Code == api.CodeForbidden {
			return api.Errorf(api.CodeForbidden, "a %s cannot add images to #%d: %s", a.Role, n, imageRule)
		}
		return err
	}
	if _, err := loadTicket(q, n); err != nil {
		return err
	}
	if size > images.MaxBytes {
		return api.Errorf(api.CodeTooLarge, "an image has at most %d MiB; this one has %.1f MiB", images.MaxBytes>>20, float64(size)/(1<<20))
	}
	var count int
	var total int64
	if err := q.QueryRow(`SELECT COUNT(*) FROM images WHERE ticket_id = ? AND expired_at IS NULL`, n).Scan(&count); err != nil {
		return err
	}
	if count >= images.MaxPerTicket {
		return api.Errorf(api.CodeConflict, "#%d already has %d images, the most a ticket keeps; the user can delete some first", n, images.MaxPerTicket)
	}
	if err := q.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM images WHERE expired_at IS NULL`).Scan(&total); err != nil {
		return err
	}
	if total+size > images.MaxTotalBytes {
		return api.Errorf(api.CodeConflict, "the stored images take %d MiB of the %d MiB omo keeps; the user can delete some first", total>>20, images.MaxTotalBytes>>20)
	}
	return nil
}

// CheckImageUpload says, before anything is read or written, whether a may
// add an image of size bytes to ticket n (size 0: not known yet). AddImage
// checks the same again.
func (b *Board) CheckImageUpload(ctx context.Context, a Actor, n int64, size int64) error {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return err
	}
	return b.read(ctx, func(tx *sql.Tx) error { return mayAddImage(tx, a, n, size) })
}

// AddImage records an image whose file is stored, on ticket n, by a, and
// emits image.added. It refuses what CheckImageUpload refuses; the caller
// then removes the file.
func (b *Board) AddImage(ctx context.Context, a Actor, n int64, img NewImage) (api.Image, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return api.Image{}, err
	}
	if a.Role == api.RoleSystem {
		return api.Image{}, api.Errorf(api.CodeForbidden, "omo itself adds no images")
	}
	if !images.ValidID(img.ID) || images.Extension(img.ContentType) == "" || img.Size <= 0 {
		return api.Image{}, api.Errorf(api.CodeValidation, "not an image omo stores")
	}
	var out api.Image
	err := b.write(ctx, func(tx *sql.Tx) error {
		if err := mayAddImage(tx, a, n, img.Size); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO images (id, ticket_id, author_role, author_session, name, content_type, size, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, img.ID, n, a.Role, a.sessionArg(), img.Name, img.ContentType, img.Size, b.stamp()); err != nil {
			return err
		}
		var err error
		if out, err = loadImage(tx, img.ID); err != nil {
			return err
		}
		return emit(tx, api.KindImageAdded, n, a, imageEvent(out))
	})
	return forActor(a, out, err)
}

func imageEvent(im api.Image) api.ImageEventData {
	return api.ImageEventData{Ticket: im.Ticket, ID: im.ID, Name: im.Name, Size: im.Size}
}

// Image returns the record of image id for an actor that may read the
// image's own ticket; the ticket a reference to it was found on does not
// matter.
func (b *Board) Image(ctx context.Context, a Actor, id string) (api.Image, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return api.Image{}, err
	}
	var out api.Image
	err := b.read(ctx, func(tx *sql.Tx) error {
		im, err := loadImage(tx, id)
		if err != nil {
			return err
		}
		if err := checkRead(tx, a, int64(im.Ticket)); err != nil {
			return err
		}
		out = im
		return nil
	})
	return forActor(a, out, err)
}

// TicketImages lists the images of ticket n, oldest first, expired ones
// included, for an actor that may read the ticket.
func (b *Board) TicketImages(ctx context.Context, a Actor, n int64) ([]api.Image, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return nil, err
	}
	out := []api.Image{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		if _, err := loadTicket(tx, n); err != nil {
			return err
		}
		return each(tx, imageSelect+` WHERE i.ticket_id = ? ORDER BY i.created_at, i.id`, n, func(rows *sql.Rows) error {
			im, err := scanImage(rows)
			if err != nil {
				return err
			}
			out = append(out, im)
			return nil
		})
	})
	return forActor(a, out, err)
}

// DeleteImage removes the record of image id and emits image.deleted; the
// caller removes the file. Only the user deletes images.
func (b *Board) DeleteImage(ctx context.Context, a Actor, id string) (api.Image, error) {
	if a.Role != api.RoleUser {
		return api.Image{}, api.Errorf(api.CodeForbidden, "a %s cannot delete images: %s", a.Role, imageRule)
	}
	var out api.Image
	err := b.write(ctx, func(tx *sql.Tx) error {
		im, err := loadImage(tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM images WHERE id = ?`, id); err != nil {
			return err
		}
		out = im
		return emit(tx, api.KindImageDeleted, int64(im.Ticket), a, imageEvent(im))
	})
	return out, err
}

// ImagesToExpire lists up to limit stored images whose ticket has been done
// (completed, exported or cancelled) since before cutoff and that were
// uploaded before cutoff. A ticket moved out of Done has no closing time and
// keeps its images. For the system only.
func (b *Board) ImagesToExpire(ctx context.Context, a Actor, cutoff time.Time, limit int) ([]api.Image, error) {
	if a.Role != api.RoleSystem {
		return nil, api.Errorf(api.CodeForbidden, "only omo expires images")
	}
	out := []api.Image{}
	at := cutoff.UTC().Format(timeLayout)
	err := b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(imageSelect+` JOIN tickets t ON t.id = i.ticket_id
			WHERE i.expired_at IS NULL AND t.col = 'done' AND t.closed_at IS NOT NULL AND t.closed_at < ? AND i.created_at < ?
			ORDER BY i.created_at, i.id LIMIT ?`, at, at, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			im, err := scanImage(rows)
			if err != nil {
				return err
			}
			out = append(out, im)
		}
		return rows.Err()
	})
	return out, err
}

// ExpireImage marks image id expired and emits image.expired, after its file
// was deleted. An image already expired or gone is left as it is. For the
// system only.
func (b *Board) ExpireImage(ctx context.Context, a Actor, id string) error {
	if a.Role != api.RoleSystem {
		return api.Errorf(api.CodeForbidden, "only omo expires images")
	}
	return b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		im, err := loadImage(tx, id)
		if err != nil {
			var e *api.Error
			if errors.As(err, &e) && e.Code == api.CodeNotFound {
				return false, nil
			}
			return false, err
		}
		if im.ExpiredAt != nil {
			return false, nil
		}
		if _, err := tx.Exec(`UPDATE images SET expired_at = ? WHERE id = ?`, b.stamp(), id); err != nil {
			return false, err
		}
		return true, emit(tx, api.KindImageExpired, int64(im.Ticket), a, imageEvent(im))
	})
}

// ImageFileNames returns the file name (<id>.<ext>) of every recorded
// image, expired ones included. For the system only.
func (b *Board) ImageFileNames(ctx context.Context, a Actor) (map[string]bool, error) {
	if a.Role != api.RoleSystem {
		return nil, api.Errorf(api.CodeForbidden, "only omo lists image files")
	}
	out := map[string]bool{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id, content_type FROM images`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, ct string
			if err := rows.Scan(&id, &ct); err != nil {
				return err
			}
			out[images.FileName(id, ct)] = true
		}
		return rows.Err()
	})
	return out, err
}
