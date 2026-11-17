package skillsets

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// sourceFile, in a checkout's .git folder, names the repository and ref
// the checkout was fetched from. A checkout is a set's only while they are
// the set's: after a change of either, agents get nothing of the old
// checkout until the new source is fetched.
const sourceFile = "omo-source"

// maxSourceFile bounds the source file (an address and a ref).
const maxSourceFile = 16 << 10

type sourceRecord struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
}

// RecordSource notes in the checkout at path that it was fetched from
// repository at ref.
func RecordSource(path, repository, ref string) error {
	gitDir := filepath.Join(path, ".git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(sourceRecord{Repository: repository, Ref: ref})
	tmp := filepath.Join(gitDir, sourceFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(gitDir, sourceFile))
}

// fromSource reports whether the checkout at path was fetched from the
// repository and ref k names now.
func fromSource(path string, k api.SkillSet) bool {
	data, err := homefile.Read(filepath.Join(path, ".git", sourceFile), maxSourceFile)
	if err != nil {
		return false
	}
	var r sourceRecord
	return json.Unmarshal(data, &r) == nil && r.Repository == k.Repository && r.Ref == k.Ref
}

// checkoutOf returns the checkout of the set k in the skills folder dir
// when there is one fetched from k's repository and ref.
func checkoutOf(dir string, k api.SkillSet) (string, bool) {
	path, ok := CheckoutIn(dir)(k.Name)
	if !ok || !fromSource(path, k) {
		return "", false
	}
	return path, true
}
