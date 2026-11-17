package homezip

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// Format is the layout version of the zips this omo writes and reads.
const Format = 1

// maxManifest bounds manifest.json.
const maxManifest = 64 << 10

// parseManifest reads manifest.json strictly and checks that this omo can
// read the zip: a zip of a later format, a later database schema or a later
// omo release is refused.
func parseManifest(raw []byte, ourVersion string) (api.BackupManifest, error) {
	var m api.BackupManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, problem("%s cannot be read: it is not the manifest of an omo settings zip", ManifestName)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return m, problem("%s must hold one JSON object", ManifestName)
	}
	switch {
	case m.Format < 1:
		return m, problem("%s names no format: it is not the manifest of an omo settings zip", ManifestName)
	case m.Format > Format:
		return m, problem("this zip was made by a newer omo (zip format %d, this omo reads %d); update omo to import it", m.Format, Format)
	case m.SchemaVersion < 1:
		return m, problem("%s names no database schema", ManifestName)
	case m.SchemaVersion > db.SchemaVersion:
		return m, problem("this zip was made by a newer omo (database schema %d, this omo knows %d); update omo to import it", m.SchemaVersion, db.SchemaVersion)
	}
	if newer(m.OmoVersion, ourVersion) {
		return m, problem("this zip was made by a newer omo (%s, this is %s); update omo to import it", shown(m.OmoVersion), shown(ourVersion))
	}
	return m, nil
}

// release is a release version: v1.2.3, maybe followed by more (a
// pre-release or a git describe suffix, which are ignored).
var release = regexp.MustCompile(`^v?(\d{1,9})\.(\d{1,9})\.(\d{1,9})`)

// newer reports whether version a is a later release than b. Versions that
// are no release (a development build) compare as not newer: the zip's
// format and schema decide then.
func newer(a, b string) bool {
	x, y := release.FindStringSubmatch(a), release.FindStringSubmatch(b)
	if x == nil || y == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		p, _ := strconv.Atoi(x[i])
		q, _ := strconv.Atoi(y[i])
		if p != q {
			return p > q
		}
	}
	return false
}

// shown is a version for a message: at most 40 printable characters.
func shown(v string) string {
	out := []rune{}
	for _, r := range v {
		if r < 0x20 || r > 0x7e {
			r = '?'
		}
		out = append(out, r)
		if len(out) == 40 {
			break
		}
	}
	if len(out) == 0 {
		return "an unknown version"
	}
	return string(out)
}

// Problem is a refusal of a zip: the message says what is wrong with it.
type Problem struct{ Message string }

func (p *Problem) Error() string { return p.Message }

func problem(format string, args ...any) error {
	return &Problem{Message: fmt.Sprintf(format, args...)}
}
