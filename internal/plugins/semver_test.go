package plugins

import (
	"strings"
	"testing"
)

func TestValidVersion(t *testing.T) {
	for _, v := range []string{"1.2.3", "0.0.1", "1.2.3-alpha.1", "1.2.3+linux", "10.20.30-rc.1+build.5"} {
		if err := validVersion(v); err != nil {
			t.Errorf("validVersion(%q) = %v", v, err)
		}
	}
	for _, v := range []string{"", "1.2", "01.2.3", "1.2.3-01", "1.2.3-", "1.2.3+", "1.2.x", "v1.2.3", "1.2.3-a..b", strings.Repeat("1", 70) + ".0.0"} {
		err := validVersion(v)
		if err == nil {
			t.Errorf("validVersion(%q) accepted", v)
			continue
		}
		if err != errVersion {
			t.Errorf("refusal of %q is not the fixed text: %v", v, err)
		}
	}
}
