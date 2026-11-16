package settings

import "github.com/scolastico-dev/one-man-office/internal/api"

// DefaultImageKeepDays is how many days uploaded images are kept after their
// ticket is done, unless images.keep_days_after_done says otherwise.
const DefaultImageKeepDays = 60

// maxImageKeepDays bounds images.keep_days_after_done (ten years); 0 keeps
// images for ever.
const maxImageKeepDays = 3650

// validateImages checks the images group.
func validateImages(im api.ImageSettings) error {
	if d := im.KeepDaysAfterDone; d < 0 || d > maxImageKeepDays {
		return fieldErr("images.keep_days_after_done",
			"images.keep_days_after_done must be between 0 (keep for ever) and %d days", maxImageKeepDays)
	}
	return nil
}
