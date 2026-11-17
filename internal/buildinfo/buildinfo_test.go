package buildinfo

import "testing"

func TestSourceOfEveryVersionForm(t *testing.T) {
	const repo = "https://github.com/scolastico-dev/one-man-office"
	tests := []struct {
		version, url, commit string
	}{
		{"0.11.0-476-g8a63736", repo + "/commit/8a63736", "8a63736"},
		{"0.11.0-476-g8a63736-dirty", repo + "/commit/8a63736", "8a63736"},
		{"v1.2.3-4-gabcdef0123456789abcdef0123456789abcdef01", repo + "/commit/abcdef0123456789abcdef0123456789abcdef01", "abcdef0123456789abcdef0123456789abcdef01"},
		{"1.0.0-rc.1-12-g1234abc", repo + "/commit/1234abc", "1234abc"},
		{"0.12.0", repo + "/releases/tag/0.12.0", ""},
		{"v0.12.0", repo + "/releases/tag/v0.12.0", ""},
		{"1.0.0-rc.1", repo + "/releases/tag/1.0.0-rc.1", ""},
		{"8a63736", repo + "/commit/8a63736", "8a63736"},
		{"8a63736-dirty", repo + "/commit/8a63736", "8a63736"},
		{"nightly-0123456789abcdef0123456789abcdef01234567", repo + "/commit/0123456789abcdef0123456789abcdef01234567", "0123456789abcdef0123456789abcdef01234567"},
		{"dev", "", ""},
		{"", "", ""},
		{"0.12.0-dirty", "", ""},
		{"0.11.0-476-gXYZ", "", ""},
		{"0.11.0-476-g8a63736 extra", "", ""},
		{"0.11.0/../x", "", ""},
		{"nightly-xyz", "", ""},
		{"abc", "", ""},
	}
	for _, tc := range tests {
		got := SourceOf(tc.version)
		if got.URL != tc.url || got.Commit != tc.commit {
			t.Errorf("SourceOf(%q) = %+v, want url %q commit %q", tc.version, got, tc.url, tc.commit)
		}
	}
}
