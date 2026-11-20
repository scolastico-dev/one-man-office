// Package homefile reads files in the omo home. A file there may have been
// replaced by anyone running as the user, agents included: by a link to a
// device, a named pipe, a folder or a file far larger than any setting. Every
// read of a home file goes through Open or Read, which do not follow a link
// at the last element of the path, never block on a named pipe, accept only
// a regular file and, for Read, stop at a limit.
package homefile

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Limits of the files omo reads from its home, in bytes.
const (
	// MaxRelayState bounds private host identity, trust and receipt metadata.
	MaxRelayState = 4 << 20
	// MaxSettingsFile bounds settings.yaml and every file in projects/,
	// profiles/ and roles/.
	MaxSettingsFile = 1 << 20
	// MaxPromptFile bounds a file in prompts/: the largest prompt omo
	// stores or renders.
	MaxPromptFile = 256 << 10
	// MaxConfigFile bounds server.yaml.
	MaxConfigFile = 64 << 10
	// MaxTokenFile bounds the login token file (64 hex characters).
	MaxTokenFile = 4 << 10
	// MaxRunFile bounds the small JSON records in the run folder: the
	// runtime state and the records of running agents.
	MaxRunFile = 64 << 10
	// MaxPinsFile bounds the engine's pins (one entry per ticket branch).
	MaxPinsFile = 16 << 20
	// MaxStartupLog bounds what of the log is printed after a start in the
	// background.
	MaxStartupLog = 64 << 10
)

var (
	// ErrLink is the error of a path whose last element is a link.
	ErrLink = errors.New("is a link, not a regular file")
	// ErrNotRegular is the error of a named pipe, a device, a socket or a
	// folder.
	ErrNotRegular = errors.New("is not a regular file")
	// ErrTooLarge is the error of a file larger than the limit of Read.
	ErrTooLarge = errors.New("is larger than allowed")
)

// Open opens the regular file at path for reading. A link at the last
// element gives ErrLink, any other kind of file ErrNotRegular, both inside an
// *os.PathError. A missing file gives the error of os.Open, so
// os.IsNotExist works on it.
func Open(path string) (*os.File, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: ErrNotRegular}
	}
	return f, nil
}

// Append opens the regular file at path for appending, creating it with
// perm when it is missing. Like Open it refuses a link at the last element
// (ErrLink) and any other kind of file (ErrNotRegular), and never waits
// for a reader of a named pipe.
func Append(path string, perm os.FileMode) (*os.File, error) {
	f, err := openAppendNoFollow(path, perm)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: ErrNotRegular}
	}
	return f, nil
}

// Read returns the content of the regular file at path, as Open opens it.
// A file of more than limit bytes gives ErrTooLarge inside an *os.PathError;
// at most limit+1 bytes are read.
func Read(path string, limit int64) ([]byte, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, &os.PathError{Op: "read", Path: path, Err: ErrTooLarge}
	}
	return data, nil
}

// Refusal returns a fixed sentence for an error of Open or Read that refused
// the kind or size of a file, without the path, and false for every other
// error. limit is the limit the file was read with.
func Refusal(err error, limit int64) (string, bool) {
	switch {
	case errors.Is(err, ErrLink):
		return "the file is a link; omo reads only regular files here", true
	case errors.Is(err, ErrNotRegular):
		return "the file is not a regular file (a folder, a named pipe or a device); omo reads only regular files here", true
	case errors.Is(err, ErrTooLarge):
		return fmt.Sprintf("the file is larger than %s", Size(limit)), true
	}
	return "", false
}

// Size is n bytes in words: "1 MiB", "256 KiB" or "100 bytes".
func Size(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
