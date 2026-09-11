package plugins

import "testing"

func TestMatchVersionSupportsRequiredConstraintForms(t *testing.T) {
	tests := []struct {
		constraint string
		version    string
		want       bool
	}{
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{"^1.2.3", "1.9.0", true},
		{"^1.2.3", "2.0.0", false},
		{"^0.2.3", "0.2.9", true},
		{"^0.2.3", "0.3.0", false},
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{">=1.2.0 <2.0.0", "1.8.4", true},
		{">=1.2.0 <2.0.0", "2.0.0", false},
		{"1.x", "1.9.0", true},
		{"1.2.x", "1.3.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.constraint+"/"+tt.version, func(t *testing.T) {
			got, err := matchVersion(tt.constraint, tt.version)
			if err != nil {
				t.Fatalf("matchVersion() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("matchVersion(%q, %q) = %t, want %t", tt.constraint, tt.version, got, tt.want)
			}
		})
	}
}

func TestMatchVersionComparesPrereleasesAndIgnoresBuildMetadata(t *testing.T) {
	for _, tt := range []struct {
		constraint string
		version    string
		want       bool
	}{
		{"1.2.3", "1.2.3+linux", true},
		{"1.2.3-alpha", "1.2.3-alpha.2", false},
		{"1.2.3-alpha.2", "1.2.3-alpha.10", false},
		{">1.2.3-rc.1", "1.2.3", true},
	} {
		got, err := matchVersion(tt.constraint, tt.version)
		if err != nil {
			t.Fatalf("matchVersion(%q, %q) error = %v", tt.constraint, tt.version, err)
		}
		if got != tt.want {
			t.Fatalf("matchVersion(%q, %q) = %t, want %t", tt.constraint, tt.version, got, tt.want)
		}
	}
}

func TestMatchVersionRejectsMalformedConstraintsAndVersions(t *testing.T) {
	for _, tt := range []struct {
		constraint string
		version    string
	}{
		{"", "1.2.3"},
		{"1.2", "1.2.3"},
		{"^1.2", "1.2.3"},
		{">=1.2.0 nope", "1.2.3"},
		{"1.x", "1.2"},
		{"1.2.3", "1.2"},
		{"1.2.3", "01.2.3"},
		{"1.2.3", "1.2.3-01"},
	} {
		if _, err := matchVersion(tt.constraint, tt.version); err == nil {
			t.Fatalf("matchVersion(%q, %q) accepted malformed input", tt.constraint, tt.version)
		}
	}
}
