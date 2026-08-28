package supervisor

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

// PruneStorage removes each file whose last modification is at least
// retentionDays distinct office-activity days old. It never follows symlinks.
func (s *Supervisor) PruneStorage(retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	activity, err := db.OfficeActivityDays(s.DB)
	if err != nil {
		return 0, err
	}
	storage := filepath.Join(s.OfficeDir, ".omo", "storage")
	removed := 0
	err = filepath.WalkDir(storage, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) && path == storage {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		firstNewerDay := sort.Search(len(activity), func(i int) bool {
			return activity[i].After(info.ModTime().UTC())
		})
		if len(activity)-firstNewerDay < retentionDays {
			return nil
		}
		latest, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if latest.ModTime() != info.ModTime() || latest.Size() != info.Size() || latest.Mode() != info.Mode() {
			return nil // the file changed while this cleanup pass was inspecting it
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed++
		return nil
	})
	return removed, err
}
