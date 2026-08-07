package supervisor

import (
	"regexp"
	"testing"
)

func TestLogNameFormat(t *testing.T) {
	got := LogName("developer-jason")
	want := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-developer-jason\.log$`)
	if !want.MatchString(got) {
		t.Fatalf("log name %q does not match yyyy-mm-dd_hh-mm-<role>-<name>.log", got)
	}
}
