// Package prompts renders role prompts: embedded defaults, exported and
// overridable per office via .omo/prompts/<role>.md.
package prompts

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed templates/*.md
var templates embed.FS

// Dir is the per-office role-prompt directory. Setup exports every embedded
// prompt here so offices can tune organizational behavior without rebuilding.
const Dir = ".omo/prompts"

var Roles = []string{"ceo", "product_manager", "developer", "reviewer", "freelancer", "smokealarm", "firefighter"}

// DefaultsDigest fingerprints every embedded role prompt plus common.md.
func DefaultsDigest() (string, error) {
	h := sha256.New()
	for _, name := range append([]string{"common"}, Roles...) {
		raw, err := templates.ReadFile("templates/" + name + ".md")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00", name)
		h.Write(raw)
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

type Data struct {
	Name    string
	Role    string
	Goal    string
	Context string
	JobID   int64
	// Extensions contains the selected role preset loaded from
	// .omo/extensions. Editable templates may place it with {{.Extensions}}.
	Extensions string
}

func Render(officeDir, role string, d Data) (string, error) {
	extensions, err := loadExtensions(officeDir, role)
	if err != nil {
		return "", err
	}
	d.Extensions = extensions
	common, err := readTemplate(officeDir, "common")
	if err != nil {
		return "", err
	}
	var body []byte
	body, err = readTemplate(officeDir, role)
	if err != nil {
		return "", fmt.Errorf("unknown role %q: %w", role, err)
	}
	tmpl, err := template.New(role).Parse(string(common) + "\n" + string(body))
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, d); err != nil {
		return "", err
	}
	return out.String(), nil
}

func readTemplate(officeDir, name string) ([]byte, error) {
	override := filepath.Join(officeDir, Dir, name+".md")
	if raw, err := os.ReadFile(override); err == nil {
		return raw, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// Compatibility with the brief pre-export layout. New and re-run setups
	// always use .omo/prompts.
	legacy := filepath.Join(officeDir, "prompts", name+".md")
	if raw, err := os.ReadFile(legacy); err == nil {
		return raw, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return templates.ReadFile("templates/" + name + ".md")
}

// WriteDefaults exports common.md and every role prompt without overwriting
// local edits. Deleting one file restores the embedded fallback on next run.
func WriteDefaults(officeDir string) error {
	dir := filepath.Join(officeDir, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	names := append([]string{"common"}, Roles...)
	for _, name := range names {
		path := filepath.Join(dir, name+".md")
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		raw, err := templates.ReadFile("templates/" + name + ".md")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}
