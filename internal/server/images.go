package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/images"
)

// ImageNameHeader carries the original file name of an upload, percent-encoded
// (encodeURIComponent). The body is the file's bytes.
const ImageNameHeader = "Omo-Image-Name"

// maxImageNameHeader bounds the encoded name.
const maxImageNameHeader = 2048

// imageCSP is the policy on the bytes of an uploaded image: opened on its
// own, the response may run and load nothing.
const imageCSP = "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox; frame-ancestors 'none'"

// Expiry sweep timing and size.
const (
	imageSweepFirst   = time.Minute
	imageSweepEvery   = time.Hour
	imageSweepBatch   = 200
	imageSweepBatches = 10
)

// registerImages adds the image routes. Uploading follows the board's rule
// for comments, reading the read scope of the image's own ticket; only the
// user deletes.
func (s *Server) registerImages(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"POST /api/tickets/{n}/images", s.uploadImage, agentWrite},
		{"GET /api/tickets/{n}/images", s.listImages, agentRead},
		{"GET /api/images/{id}", s.getImage, agentRead},
		{"GET /api/images/{id}/content", s.getImageContent, agentRead},
		{"DELETE /api/images/{id}", s.deleteImage, userOnly},
	})
}

// shown fills what only the server knows of an image record: whether its
// file is still there and where it is.
func (s *Server) shown(im api.Image) api.Image {
	if im.State != api.ImageStored {
		return im
	}
	if !s.imageStore.Present(im.ID, im.ContentType) {
		im.State = api.ImageMissing
		return im
	}
	im.Path = s.imageStore.Path(im.ID, im.ContentType)
	return im
}

// imageID reads the id path value; anything but a UUID is not_found and is
// never looked up.
func imageID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if !images.ValidID(id) {
		return "", api.Errorf(api.CodeNotFound, "no image has the id %q", oneLineID(id))
	}
	return id, nil
}

// oneLineID keeps a refused id short and printable in the refusal.
func oneLineID(s string) string {
	if len(s) > 64 || !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "…"
	}
	return s
}

func (s *Server) uploadImage(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	raw := r.Header.Get(ImageNameHeader)
	if len(raw) > maxImageNameHeader {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "the %s header is longer than %d bytes", ImageNameHeader, maxImageNameHeader))
		return
	}
	name, err := url.PathUnescape(raw)
	if err != nil || !utf8.ValidString(name) {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "the %s header must be the file name, percent-encoded UTF-8", ImageNameHeader))
		return
	}
	a := actorFrom(r)
	// Who may upload, to which ticket, and the limits are checked before a
	// byte of the body is read.
	size := max(r.ContentLength, 0)
	if err := s.board.CheckImageUpload(r.Context(), a, n, size); err != nil {
		writeError(w, r, err)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, images.MaxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, r, api.Errorf(api.CodeTooLarge, "an image has at most %d MiB", images.MaxBytes>>20))
			return
		}
		writeError(w, r, api.Errorf(api.CodeBadRequest, "the upload was cut off"))
		return
	}
	if len(data) == 0 {
		writeError(w, r, api.Errorf(api.CodeValidation, "the upload is empty"))
		return
	}
	ct, err := images.Sniff(data)
	if err != nil {
		writeError(w, r, api.Errorf(api.CodeValidation, "%s", err.Error()))
		return
	}
	id := images.NewID()
	if err := s.imageStore.Write(id, ct, data); err != nil {
		writeError(w, r, fmt.Errorf("store image: %w", err))
		return
	}
	im, err := s.board.AddImage(r.Context(), a, n, board.NewImage{ID: id, Name: images.CleanName(name, ct), ContentType: ct, Size: int64(len(data))})
	if err != nil {
		if rmErr := s.imageStore.Remove(id, ct); rmErr != nil {
			logf(r, "remove the file of a refused image: %v", rmErr)
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.shown(im))
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	list, err := s.board.TicketImages(r.Context(), actorFrom(r), n)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for i := range list {
		list[i] = s.shown(list[i])
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	id, err := imageID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	im, err := s.board.Image(r.Context(), actorFrom(r), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.shown(im))
}

// getImageContent serves the bytes of an image to a caller that may read
// the image's own ticket: with the type found when it was uploaded, never
// sniffed by the browser, as an inline picture under a generated name, under
// a policy that lets nothing run, cached privately for as long as the id
// lives (an id never names other bytes).
func (s *Server) getImageContent(w http.ResponseWriter, r *http.Request) {
	id, err := imageID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	im, err := s.board.Image(r.Context(), actorFrom(r), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if im.State == api.ImageExpired {
		writeError(w, r, api.Errorf(api.CodeNotFound, "image %s expired: its ticket was done long enough ago", id))
		return
	}
	data, err := s.imageStore.Read(im.ID, im.ContentType)
	if err != nil {
		writeError(w, r, api.Errorf(api.CodeNotFound, "the file of image %s is missing", id))
		return
	}
	h := w.Header()
	h.Set("Content-Type", im.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("Content-Disposition", contentDisposition(im))
	h.Set("Content-Security-Policy", imageCSP)
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(data)
	}
}

// contentDisposition is "inline" with a name omo makes up from the id and
// the type, and the original name only as RFC 5987 filename*, every byte
// outside the attribute characters percent-encoded.
func contentDisposition(im api.Image) string {
	return fmt.Sprintf(`inline; filename="image-%s.%s"; filename*=UTF-8''%s`, im.ID, images.Extension(im.ContentType), rfc5987(im.Name))
}

func rfc5987(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	id, err := imageID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a := actorFrom(r)
	im, err := s.board.Image(r.Context(), a, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.imageStore.Remove(im.ID, im.ContentType); err != nil {
		writeError(w, r, fmt.Errorf("remove image file: %w", err))
		return
	}
	if _, err := s.board.DeleteImage(r.Context(), a, id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runImageSweep expires images on a timer until ctx ends: a minute after
// the start, then every hour.
func (s *Server) runImageSweep(ctx context.Context) {
	timer := time.NewTimer(imageSweepFirst)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.sweepImages(ctx, time.Now())
		timer.Reset(imageSweepEvery)
	}
}

// sweepImages deletes the files of images whose ticket has been done (or
// cancelled) for longer than images.keep_days_after_done at now and marks
// their records expired; 0 days keeps every image. At most
// imageSweepBatches batches of imageSweepBatch images go per call; the rest
// waits for the next one. It also counts the files in the images folder that
// have no record and logs the number when it changed: such files are left
// alone, omo deletes only what it wrote. It returns how many images expired.
func (s *Server) sweepImages(ctx context.Context, now time.Time) int {
	expired := 0
	if days := s.settings.General().Images.KeepDaysAfterDone; days > 0 {
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
		for range imageSweepBatches {
			due, err := s.board.ImagesToExpire(ctx, board.System, cutoff, imageSweepBatch)
			if err != nil {
				if ctx.Err() == nil {
					s.log.Printf("images: list the images to expire: %v", err)
				}
				return expired
			}
			for _, im := range due {
				if err := s.imageStore.Remove(im.ID, im.ContentType); err != nil {
					s.log.Printf("images: delete the file of expired image %s: %v", im.ID, err)
					continue
				}
				if err := s.board.ExpireImage(ctx, board.System, im.ID); err != nil {
					s.log.Printf("images: mark image %s expired: %v", im.ID, err)
					continue
				}
				expired++
			}
			if len(due) < imageSweepBatch {
				break
			}
		}
	}
	known, err := s.board.ImageFileNames(ctx, board.System)
	if err != nil {
		return expired
	}
	stray, err := s.imageStore.Stray(func(name string) bool { return known[name] })
	if err != nil {
		s.log.Printf("images: read the images folder: %v", err)
		return expired
	}
	if old := s.imageStray.Swap(int64(stray)); old != int64(stray) && stray > 0 {
		s.log.Printf("images: %d files in %s have no record; omo leaves them alone", stray, s.imageStore.Dir())
	}
	return expired
}
