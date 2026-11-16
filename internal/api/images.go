package api

import "time"

// ImageRefScheme starts the target of an uploaded image in Markdown:
// ![<original name>](omo-image:<id>). The id is the image's UUID; the alt
// text keeps the name it was uploaded with.
const ImageRefScheme = "omo-image:"

// Values of Image.State.
const (
	// ImageStored: the file is in the images folder of the omo home.
	ImageStored = "stored"
	// ImageExpired: the file was deleted because its ticket had been done
	// for longer than the general setting images.keep_days_after_done.
	ImageExpired = "expired"
	// ImageMissing: the record exists but the file is gone (or is not a
	// regular file omo can read).
	ImageMissing = "missing"
)

// Image is an uploaded image: the answer of POST /api/tickets/{n}/images,
// GET /api/images/{id} and the items of GET /api/tickets/{n}/images. Name
// is the file name it was uploaded with (cleaned: no folders, no hidden
// characters); the file itself is stored as <id>.<extension of its type>.
// Path is the absolute local path of that file, empty unless State is
// stored. Markdown is the reference to write into a description or comment.
type Image struct {
	ID          string     `json:"id"`
	Ticket      int        `json:"ticket"`
	Name        string     `json:"name"`
	ContentType string     `json:"content_type"`
	Size        int64      `json:"size"`
	Author      Actor      `json:"author"`
	CreatedAt   time.Time  `json:"created_at"`
	State       string     `json:"state"`
	ExpiredAt   *time.Time `json:"expired_at,omitempty"`
	Path        string     `json:"path"`
	Markdown    string     `json:"markdown"`
}

// ImageEventData is the data of image.added, image.deleted and
// image.expired: ids, the name and the size only.
type ImageEventData struct {
	Ticket int    `json:"ticket"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
}

// ImageSettings is the general settings group images.
type ImageSettings struct {
	// KeepDaysAfterDone is how many days the uploaded images of a ticket
	// are kept once the ticket is done (or cancelled); 0 keeps them for
	// ever. An image uploaded after its ticket was closed is kept as long
	// from its upload.
	KeepDaysAfterDone int `yaml:"keep_days_after_done" json:"keep_days_after_done"`
}
