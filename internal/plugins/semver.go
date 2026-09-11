package plugins

import (
	"fmt"
	"strings"
)

type semVersion struct {
	major      string
	minor      string
	patch      string
	prerelease []semIdentifier
}

type semIdentifier struct {
	value   string
	numeric bool
}

type versionPredicate struct {
	op      string
	version semVersion
}

// MatchVersion reports whether a concrete SemVer satisfies a space-separated
// dependency constraint. The supported forms are exact versions, caret and
// tilde ranges, comparisons, comparison chains, and x wildcards.
func MatchVersion(constraint, version string) (bool, error) {
	return matchVersion(constraint, version)
}

func matchVersion(constraint, version string) (bool, error) {
	got, err := parseSemVersion(version)
	if err != nil {
		return false, fmt.Errorf("invalid version %q: %w", version, err)
	}
	predicates, err := parseVersionConstraint(constraint)
	if err != nil {
		return false, err
	}
	for _, predicate := range predicates {
		if !predicate.matches(got) {
			return false, nil
		}
	}
	return true, nil
}

func parseSemVersion(raw string) (semVersion, error) {
	if raw == "" {
		return semVersion{}, fmt.Errorf("version is required")
	}
	coreAndBuild := strings.SplitN(raw, "+", 2)
	coreAndPre := strings.SplitN(coreAndBuild[0], "-", 2)
	core := strings.Split(coreAndPre[0], ".")
	if len(core) != 3 {
		return semVersion{}, fmt.Errorf("version must contain major, minor, and patch")
	}
	major, err := parseNumericIdentifier(core[0], "major")
	if err != nil {
		return semVersion{}, err
	}
	minor, err := parseNumericIdentifier(core[1], "minor")
	if err != nil {
		return semVersion{}, err
	}
	patch, err := parseNumericIdentifier(core[2], "patch")
	if err != nil {
		return semVersion{}, err
	}
	result := semVersion{major: major, minor: minor, patch: patch}
	if len(coreAndPre) == 2 {
		if coreAndPre[1] == "" {
			return semVersion{}, fmt.Errorf("prerelease must not be empty")
		}
		for _, rawIdentifier := range strings.Split(coreAndPre[1], ".") {
			identifier, err := parsePrereleaseIdentifier(rawIdentifier)
			if err != nil {
				return semVersion{}, err
			}
			result.prerelease = append(result.prerelease, identifier)
		}
	}
	if len(coreAndBuild) == 2 {
		if coreAndBuild[1] == "" {
			return semVersion{}, fmt.Errorf("build metadata must not be empty")
		}
		for _, identifier := range strings.Split(coreAndBuild[1], ".") {
			if !validIdentifier(identifier) {
				return semVersion{}, fmt.Errorf("invalid build identifier %q", identifier)
			}
		}
	}
	return result, nil
}

func parseNumericIdentifier(raw, label string) (string, error) {
	if raw == "" || !allDigits(raw) || (len(raw) > 1 && raw[0] == '0') {
		return "", fmt.Errorf("invalid %s version identifier %q", label, raw)
	}
	return raw, nil
}

func parsePrereleaseIdentifier(raw string) (semIdentifier, error) {
	if !validIdentifier(raw) {
		return semIdentifier{}, fmt.Errorf("invalid prerelease identifier %q", raw)
	}
	if allDigits(raw) {
		if len(raw) > 1 && raw[0] == '0' {
			return semIdentifier{}, fmt.Errorf("numeric prerelease identifier %q has leading zero", raw)
		}
		return semIdentifier{value: raw, numeric: true}, nil
	}
	return semIdentifier{value: raw}, nil
}

func validIdentifier(raw string) bool {
	if raw == "" {
		return false
	}
	for _, char := range raw {
		if !(char >= '0' && char <= '9' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char == '-') {
			return false
		}
	}
	return true
}

func allDigits(raw string) bool {
	if raw == "" {
		return false
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func parseVersionConstraint(raw string) ([]versionPredicate, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("version constraint is required")
	}
	var predicates []versionPredicate
	for _, token := range strings.Fields(raw) {
		if strings.Contains(token, ",") {
			return nil, fmt.Errorf("invalid version constraint %q", raw)
		}
		if strings.HasPrefix(token, "^") || strings.HasPrefix(token, "~") {
			if len(token) == 1 {
				return nil, fmt.Errorf("invalid version constraint %q", raw)
			}
			version, err := parseSemVersion(token[1:])
			if err != nil {
				return nil, fmt.Errorf("invalid version constraint %q: %w", raw, err)
			}
			predicates = append(predicates, versionPredicate{op: ">=", version: version})
			upper := version
			if token[0] == '^' {
				switch {
				case version.major != "0":
					next, err := incrementVersionPart(version.major, "major")
					if err != nil {
						return nil, fmt.Errorf("invalid version constraint %q: %w", raw, err)
					}
					upper = semVersion{major: next, minor: "0", patch: "0"}
				case version.minor != "0":
					next, err := incrementVersionPart(version.minor, "minor")
					if err != nil {
						return nil, fmt.Errorf("invalid version constraint %q: %w", raw, err)
					}
					upper = semVersion{major: version.major, minor: next, patch: "0"}
				default:
					next, err := incrementVersionPart(version.patch, "patch")
					if err != nil {
						return nil, fmt.Errorf("invalid version constraint %q: %w", raw, err)
					}
					upper = semVersion{major: version.major, minor: version.minor, patch: next}
				}
			} else {
				next, err := incrementVersionPart(version.minor, "minor")
				if err != nil {
					return nil, fmt.Errorf("invalid version constraint %q: %w", raw, err)
				}
				upper = semVersion{major: version.major, minor: next, patch: "0"}
			}
			predicates = append(predicates, versionPredicate{op: "<", version: upper})
			continue
		}
		if strings.Contains(token, "x") || strings.Contains(token, "X") {
			wildcards, err := parseWildcardConstraint(token)
			if err != nil {
				return nil, fmt.Errorf("invalid version constraint %q: %w", raw, err)
			}
			predicates = append(predicates, wildcards...)
			continue
		}
		op := "="
		value := token
		for _, candidate := range []string{">=", "<=", ">", "<"} {
			if strings.HasPrefix(token, candidate) {
				op, value = candidate, strings.TrimPrefix(token, candidate)
				break
			}
		}
		version, err := parseSemVersion(value)
		if err != nil {
			return nil, fmt.Errorf("invalid version constraint %q: %w", raw, err)
		}
		predicates = append(predicates, versionPredicate{op: op, version: version})
	}
	return predicates, nil
}

func parseWildcardConstraint(raw string) ([]versionPredicate, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return nil, fmt.Errorf("wildcard must be 1.x or 1.2.x")
	}
	if parts[len(parts)-1] != "x" {
		return nil, fmt.Errorf("wildcard must end in .x")
	}
	major, err := parseNumericIdentifier(parts[0], "major")
	if err != nil {
		return nil, err
	}
	lower := semVersion{major: major, minor: "0", patch: "0"}
	nextMajor, err := incrementVersionPart(major, "major")
	if err != nil {
		return nil, err
	}
	upper := semVersion{major: nextMajor, minor: "0", patch: "0"}
	if len(parts) == 3 {
		minor, err := parseNumericIdentifier(parts[1], "minor")
		if err != nil {
			return nil, err
		}
		nextMinor, err := incrementVersionPart(minor, "minor")
		if err != nil {
			return nil, err
		}
		lower.minor = minor
		upper = semVersion{major: major, minor: nextMinor, patch: "0"}
	}
	return []versionPredicate{{op: ">=", version: lower}, {op: "<", version: upper}}, nil
}

func incrementVersionPart(value, label string) (string, error) {
	digits := []byte(value)
	for i := len(digits) - 1; i >= 0; i-- {
		if digits[i] < '9' {
			digits[i]++
			return string(digits), nil
		}
		digits[i] = '0'
	}
	if len(digits) == 0 {
		return "", fmt.Errorf("%s version identifier is empty", label)
	}
	return "1" + string(digits), nil
}

func (p versionPredicate) matches(version semVersion) bool {
	comparison := compareSemVersion(version, p.version)
	switch p.op {
	case "=":
		return comparison == 0
	case ">":
		return comparison > 0
	case ">=":
		return comparison >= 0
	case "<":
		return comparison < 0
	case "<=":
		return comparison <= 0
	default:
		return false
	}
}

func compareSemVersion(left, right semVersion) int {
	for _, pair := range [][2]string{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if comparison := compareNumericStrings(pair[0], pair[1]); comparison != 0 {
			return comparison
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(left.prerelease) && i < len(right.prerelease); i++ {
		leftID, rightID := left.prerelease[i], right.prerelease[i]
		if leftID.numeric && rightID.numeric {
			if comparison := compareNumericStrings(leftID.value, rightID.value); comparison != 0 {
				return comparison
			}
		} else if leftID.numeric != rightID.numeric {
			if leftID.numeric {
				return -1
			}
			return 1
		} else if leftID.value < rightID.value {
			return -1
		} else if leftID.value > rightID.value {
			return 1
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func compareNumericStrings(left, right string) int {
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
