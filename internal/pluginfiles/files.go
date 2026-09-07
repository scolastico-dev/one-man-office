// Package pluginfiles owns the filesystem protocol shared by plugin updates
// and runtime snapshots. Cooperating processes lock the installation root
// before inspecting or replacing active directories.
package pluginfiles

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
)

func Lock(ctx context.Context, root string) (*filelock.Lock, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	return filelock.Acquire(ctx, filepath.Join(root, ".update.lock"))
}

// CopyTree creates independent regular files, never hard links or symlinks.
// Git metadata is excluded from both active installations and snapshots.
func CopyTree(source, target string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == ".git" || strings.HasPrefix(filepath.ToSlash(rel), ".git/") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dest := filepath.Join(target, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not supported: %s", rel)
		}
		if info.IsDir() {
			return os.MkdirAll(dest, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", rel)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
