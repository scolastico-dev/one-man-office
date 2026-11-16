package settings

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestImageKeepDays(t *testing.T) {
	s, home, _ := openTest(t)
	if got := s.General().Images.KeepDaysAfterDone; got != 60 {
		t.Fatalf("default keep_days_after_done = %d, want 60", got)
	}
	// An older file without the group keeps the default; 0 is "for ever".
	path := filepath.Join(home, "settings.yaml")
	writeFile(t, path, "max_running_agents: 3\n")
	s.Reload()
	if got := s.General().Images.KeepDaysAfterDone; got != 60 || len(s.Errors()) != 0 {
		t.Fatalf("older file: %d, errors %+v", got, s.Errors())
	}
	writeFile(t, path, "images:\n  keep_days_after_done: 0\n")
	s.Reload()
	if got := s.General().Images.KeepDaysAfterDone; got != 0 || len(s.Errors()) != 0 {
		t.Fatalf("0: %d, errors %+v", got, s.Errors())
	}
	for _, bad := range []int{-1, 3651} {
		g := Defaults()
		g.Images.KeepDaysAfterDone = bad
		err := s.SetGeneral(g)
		var ae *api.Error
		if !asAPIError(err, &ae) || ae.Code != api.CodeInvalidSettings || !strings.Contains(ae.Message, "images.keep_days_after_done") {
			t.Errorf("%d: err = %v", bad, err)
		}
		if d, ok := ae.Details.(api.FieldDetail); !ok || d.Field != "images.keep_days_after_done" {
			t.Errorf("%d: details %+v", bad, ae.Details)
		}
	}
	g := Defaults()
	g.Images.KeepDaysAfterDone = 3650
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
}
