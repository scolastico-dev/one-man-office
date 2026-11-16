package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/images"
)

func addAIImageCommands(ai *cobra.Command) {
	image := &cobra.Command{
		Use:     "image",
		Short:   "Images on tickets: add one, then write its reference into a comment or description",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		Example: "  omo ai image add 12 ./screenshot.png",
	}
	image.AddCommand(&cobra.Command{
		Use:   "add N FILE",
		Short: "Upload an image file to ticket N and print its Markdown reference",
		Long: `Upload a PNG, JPEG, GIF or WebP file (at most 10 MiB) to ticket N, where you
may comment. omo stores it in its images folder under a new id and prints the
Markdown reference to paste into a comment or description, for example
![screenshot.png](omo-image:<id>), and the local path of the stored file.
When you read a ticket or its comments, every such reference is followed by
the local path of the file, which you can open.`,
		Example: "  omo ai image add 12 ./screenshot.png",
		Args:    cobra.ExactArgs(2),
		RunE: aiRun(func(a *aiCall, args []string) error {
			n, err := ticketNumber(args[0])
			if err != nil {
				return err
			}
			data, err := readImageFile(args[1])
			if err != nil {
				return err
			}
			im, err := a.client.UploadImage(a.ctx, n, filepath.Base(args[1]), data)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) {
				fmt.Fprintf(w, "image %s added to #%d; paste this reference into a comment or description:\n", im.ID, n)
				fmt.Fprintln(w, bodyIndent+visibleLine(im.Markdown))
				if im.Path != "" {
					fmt.Fprintf(w, "local file: %s\n", visibleLine(im.Path))
				}
			})
		}),
	})
	ai.AddCommand(image)
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

// withImagePaths is the one place that rewrites text an agent reads (a
// description, a comment) so that every image reference is followed by the
// local path of its file, or says that the image expired or is missing. An
// image the caller may not read (its ticket is outside the caller's scope)
// is missing for it. With --json nothing is rewritten.
func (a *aiCall) withImagePaths(text string) string {
	if a.json {
		return text
	}
	return images.Annotate(text, a.imageNote)
}

// imageNote looks image id up once per command.
func (a *aiCall) imageNote(id string) images.Note {
	if n, ok := a.imageNotes[id]; ok {
		return n
	}
	n := images.Note{Missing: true}
	if im, err := a.client.Image(a.ctx, id); err == nil {
		switch {
		case im.State == api.ImageStored && im.Path != "":
			n = images.Note{Path: im.Path}
		case im.State == api.ImageExpired:
			n = images.Note{Expired: true}
		}
	}
	if a.imageNotes == nil {
		a.imageNotes = map[string]images.Note{}
	}
	a.imageNotes[id] = n
	return n
}
