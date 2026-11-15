// Package folderlist lists the sub-folders of one folder for the folder
// picker. It names folders only (never files or their contents), reads a
// bounded number of entries, never follows a link found inside the listed
// folder, and reports clean absolute paths.
package folderlist

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// MaxEntries is the most folders one listing holds.
const MaxEntries = 2000

// maxExamined bounds the directory entries read (folders and files alike),
// so a folder with millions of files cannot hold a request.
const maxExamined = 50000

const batch = 256

// Entry is one sub-folder. Name is for display: characters that are hidden
// or reorder text (textsafe.IsHidden) are written as \u{…}. Path is the
// exact, absolute path of the folder.
type Entry struct {
	Name, Path string
	Repository bool // holds a .git folder (not a link, not a .git file)
	Hidden     bool // the name starts with a dot
	Link       bool // a link to a folder; never followed, never a repository
}

// Listing is the answer for one folder.
type Listing struct {
	Path, Parent, Home string
	Folders            []Entry
	More               bool // entries were left out at the limit
}

var (
	ErrNotAbsolute = errors.New("the path must be absolute")
	ErrNotFolder   = errors.New("not a folder")
)

func clampMax(max int) int {
	if max <= 0 || max > MaxEntries {
		return MaxEntries
	}
	return max
}

// List lists the sub-folders of path: "" is the home folder, a leading "~"
// is the home folder. The folder itself may be reached through a link (the
// user typed it); links inside it are listed as links and not followed.
// Files are left out. At most max folders (and never more than MaxEntries)
// are returned; More is set when others were left out. The read stops and
// returns ctx.Err() when ctx ends.
func List(ctx context.Context, path string, max int) (Listing, error) {
	max = clampMax(max)
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		home = ""
	}
	switch {
	case path == "" || path == "~":
		if home == "" {
			return Listing{}, errors.New("the home folder is unknown")
		}
		path = home
	case strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~"+string(filepath.Separator)):
		if home == "" {
			return Listing{}, errors.New("the home folder is unknown")
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return Listing{}, ErrNotAbsolute
	}
	path = filepath.Clean(path)

	type result struct {
		l   Listing
		err error
	}
	done := make(chan result, 1)
	go func() {
		l, err := read(ctx, path, max)
		l.Home = home
		done <- result{l, err}
	}()
	select {
	case <-ctx.Done():
		return Listing{}, ctx.Err()
	case r := <-done:
		if r.err != nil {
			return Listing{}, r.err
		}
		return r.l, nil
	}
}

func read(ctx context.Context, path string, max int) (Listing, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Listing{}, fmt.Errorf("%s: %w", path, err)
	}
	if !info.IsDir() {
		return Listing{}, fmt.Errorf("%s: %w", path, ErrNotFolder)
	}
	dir, err := os.Open(path)
	if err != nil {
		return Listing{}, fmt.Errorf("%s: %w", path, err)
	}
	defer dir.Close()

	l := Listing{Path: path, Folders: []Entry{}}
	if parent := filepath.Dir(path); parent != path {
		l.Parent = parent
	}
	examined := 0
	for {
		if err := ctx.Err(); err != nil {
			return Listing{}, err
		}
		entries, err := dir.ReadDir(batch)
		for _, d := range entries {
			examined++
			e, ok := folderEntry(path, d)
			if !ok {
				continue
			}
			if len(l.Folders) >= max {
				l.More = true
				break
			}
			l.Folders = append(l.Folders, e)
		}
		if l.More {
			break
		}
		if examined >= maxExamined {
			l.More = true
			break
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Listing{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	sortEntries(l.Folders)
	return l, nil
}

func folderEntry(parent string, d os.DirEntry) (Entry, bool) {
	name := d.Name()
	if !utf8.ValidString(name) {
		return Entry{}, false // cannot be carried in JSON without changing it
	}
	full := filepath.Join(parent, name)
	e := Entry{Name: display(name), Path: full, Hidden: strings.HasPrefix(name, ".")}
	switch {
	case d.IsDir():
		if g, err := os.Lstat(filepath.Join(full, ".git")); err == nil && g.IsDir() {
			e.Repository = true
		}
	case d.Type()&os.ModeSymlink != 0:
		target, err := os.Stat(full)
		if err != nil || !target.IsDir() {
			return Entry{}, false
		}
		e.Link = true
	default:
		return Entry{}, false
	}
	return e, true
}

// display writes the hidden characters of a name as \u{…}, the way the
// board's visibleText does.
func display(name string) string {
	clean := true
	for _, r := range name {
		if textsafe.IsHidden(r) {
			clean = false
			break
		}
	}
	if clean {
		return name
	}
	var b strings.Builder
	for _, r := range name {
		if textsafe.IsHidden(r) {
			b.WriteString(`\u{` + strings.ToUpper(strconv.FormatInt(int64(r), 16)) + `}`)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.Hidden != b.Hidden {
			return !a.Hidden
		}
		la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if la != lb {
			return la < lb
		}
		return a.Name < b.Name
	})
}
