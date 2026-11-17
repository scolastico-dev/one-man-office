package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// licenceID is the licence omo is distributed under.
const licenceID = "AGPL-3.0-or-later"

// TestLicenceIsStatedConsistently keeps the README, NOTICE and the web
// app's package metadata on the same licence, and LICENSE the plain text of
// version 3.
func TestLicenceIsStatedConsistently(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if !strings.Contains(read("LICENSE"), "GNU AFFERO GENERAL PUBLIC LICENSE\n                       Version 3, 19 November 2007") {
		t.Error("LICENSE is not the text of the GNU AGPL version 3")
	}
	notice := read("NOTICE")
	for _, want := range []string{"either version 3 of the License, or\n(at your option) any later version", "SPDX-License-Identifier: " + licenceID} {
		if !strings.Contains(notice, want) {
			t.Errorf("NOTICE lacks %q", want)
		}
	}
	if !strings.Contains(read("README.md"), "`"+licenceID+"`") {
		t.Errorf("README.md does not name %s", licenceID)
	}
	var pkg struct {
		License string `json:"license"`
	}
	if err := json.Unmarshal([]byte(read("web/package.json")), &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.License != licenceID {
		t.Errorf("web/package.json license = %q, want %s", pkg.License, licenceID)
	}
}
