package aicmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/images"
)

type imageAddIn struct {
	Ticket int    `json:"ticket" arg:"0" jsonschema:"the ticket number"`
	File   string `json:"path" arg:"1" jsonschema:"the local path of a PNG, JPEG, GIF or WebP file of at most 10 MiB"`
}

func imageCommands() []Command {
	return []Command{
		Def("image add", "Upload an image file to ticket N and print its Markdown reference", everyone, imageAdd).help("add N FILE",
			`Upload a PNG, JPEG, GIF or WebP file (at most 10 MiB) to ticket N, where you
may comment. omo stores it in its images folder under a new id and prints the
Markdown reference to paste into a comment or description, for example
![screenshot.png](omo-image:<id>), and the local path of the stored file.
When you read a ticket or its comments, every such reference is followed by
the local path of the file, which you can open.`,
			"  omo ai image add 12 ./screenshot.png"),
	}
}

func imageAdd(ctx context.Context, c *apiclient.Client, in imageAddIn) (Result, error) {
	data, err := readImageFile(in.File)
	if err != nil {
		return Result{}, err
	}
	im, err := c.UploadImage(ctx, in.Ticket, filepath.Base(in.File), data)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		fmt.Fprintf(w, "image %s added to #%d; paste this reference into a comment or description:\n", im.ID, in.Ticket)
		fmt.Fprintln(w, bodyIndent+visibleLine(im.Markdown))
		if im.Path != "" {
			fmt.Fprintf(w, "local file: %s\n", visibleLine(im.Path))
		}
	})), nil
}

// readImageFile reads a local regular file of at most images.MaxBytes.
func readImageFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, errors.Unwrap(err))
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > images.MaxBytes {
		return nil, fmt.Errorf("%s has %.1f MiB; omo takes images of at most %d MiB", path, float64(info.Size())/(1<<20), images.MaxBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, images.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > images.MaxBytes {
		return nil, fmt.Errorf("%s grew past %d MiB while it was read", path, images.MaxBytes>>20)
	}
	return data, nil
}

// imageNotes is the one place that rewrites text an agent reads (a
// description, a comment) so that every image reference is followed by the
// local path of its file, or says that the image expired or is missing. An
// image the caller may not read (its ticket is outside the caller's scope)
// is missing for it. Each image is looked up once per command. It never
// changes a Result's Value.
type imageNotes struct {
	ctx   context.Context
	c     *apiclient.Client
	notes map[string]images.Note
}

func newImageNotes(ctx context.Context, c *apiclient.Client) *imageNotes {
	return &imageNotes{ctx: ctx, c: c, notes: map[string]images.Note{}}
}

func (n *imageNotes) annotate(text string) string {
	return images.Annotate(text, n.note)
}

func (n *imageNotes) note(id string) images.Note {
	if note, ok := n.notes[id]; ok {
		return note
	}
	note := images.Note{Missing: true}
	if im, err := n.c.Image(n.ctx, id); err == nil {
		switch {
		case im.State == api.ImageStored && im.Path != "":
			note = images.Note{Path: im.Path}
		case im.State == api.ImageExpired:
			note = images.Note{Expired: true}
		}
	}
	n.notes[id] = note
	return note
}
