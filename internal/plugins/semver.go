package plugins

import (
	"errors"
	"strings"
)

// errVersion is the refusal of a version that is not a semantic version.
var errVersion = errors.New("version must be a semantic version such as 1.2.3")

// maxVersionBytes bounds a manifest's version.
const maxVersionBytes = 64

// validVersion checks that raw is a semantic version
// (major.minor.patch[-prerelease][+build], numbers without leading zeros).
// Its refusals never quote raw.
func validVersion(raw string) error {
	if raw == "" || len(raw) > maxVersionBytes {
		return errVersion
	}
	rest, build, hasBuild := strings.Cut(raw, "+")
	core, pre, hasPre := strings.Cut(rest, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return errVersion
	}
	for _, p := range parts {
		if !number(p) {
			return errVersion
		}
	}
	if hasPre {
		for _, id := range strings.Split(pre, ".") {
			if !identifier(id) || (allDigits(id) && !number(id)) {
				return errVersion
			}
		}
	}
	if hasBuild {
		for _, id := range strings.Split(build, ".") {
			if !identifier(id) {
				return errVersion
			}
		}
	}
	return nil
}

// number is a numeric identifier without a leading zero.
func number(s string) bool {
	return allDigits(s) && (len(s) == 1 || s[0] != '0')
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-') {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
