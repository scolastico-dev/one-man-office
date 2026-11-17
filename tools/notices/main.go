// Command notices writes THIRD_PARTY_NOTICES: the licence texts of the Go
// standard library and of every Go module linked into omo on any release
// platform, read from the module cache, followed by those of the npm
// packages in the web app's bundle, which the web build writes to
// web/third-party-notices.txt. A module or package without a licence file
// is an error, so the file is complete or not written.
//
// Run from the repository root: go run ./tools/notices -o THIRD_PARTY_NOTICES
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// platforms are the GOOS values of the release builds; their dependencies
// differ.
var platforms = []string{"linux", "darwin", "windows"}

var licenceFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)([-._].*)?$`)

type module struct{ path, version, dir string }

func main() {
	out := flag.String("o", "THIRD_PARTY_NOTICES", "file to write")
	web := flag.String("web", "web/third-party-notices.txt", "the npm notices of the web build; empty to leave the web app out")
	flag.Parse()
	if err := run(*out, *web); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run(out, web string) error {
	var b bytes.Buffer
	b.WriteString("omo includes the third-party software listed below. Each entry\n" +
		"gives its name, version and licence text. omo itself is licensed under\n" +
		"AGPL-3.0-or-later; see LICENSE and NOTICE.\n\n")

	goroot, err := goEnv("GOROOT")
	if err != nil {
		return err
	}
	goVersion, err := goEnv("GOVERSION")
	if err != nil {
		return err
	}
	if err := section(&b, "Go standard library", goVersion, "Go", goroot, []string{"LICENSE", "PATENTS"}); err != nil {
		return err
	}
	mods, err := modules()
	if err != nil {
		return err
	}
	for _, m := range mods {
		files, err := licenceFiles(m.dir)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("%s %s has no licence file in %s", m.path, m.version, m.dir)
		}
		if err := section(&b, m.path, m.version, "Go module", m.dir, files); err != nil {
			return err
		}
	}
	if web != "" {
		raw, err := os.ReadFile(web)
		if err != nil {
			return fmt.Errorf("%w (build the web app first: make web)", err)
		}
		b.Write(raw)
	}
	return os.WriteFile(out, b.Bytes(), 0o644)
}

func goEnv(name string) (string, error) {
	raw, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", name, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// modules lists the modules linked into ./cmd/omo on every platform.
func modules() ([]module, error) {
	seen := map[string]module{}
	for _, goos := range platforms {
		cmd := exec.Command("go", "list", "-deps", "-f",
			`{{with .Module}}{{if not .Main}}{{with .Replace}}{{.Path}}	{{.Version}}	{{.Dir}}{{else}}{{.Path}}	{{.Version}}	{{.Dir}}{{end}}{{end}}{{end}}`,
			"./cmd/omo")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
		cmd.Stderr = os.Stderr
		raw, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list for %s: %w", goos, err)
		}
		for line := range strings.Lines(string(raw)) {
			f := strings.Split(strings.TrimSpace(line), "\t")
			if len(f) != 3 {
				continue
			}
			if f[2] == "" {
				return nil, fmt.Errorf("%s %s is not in the module cache (go mod download)", f[0], f[1])
			}
			seen[f[0]+"@"+f[1]] = module{path: f[0], version: f[1], dir: f[2]}
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("go list found no module")
	}
	out := make([]module, 0, len(seen))
	for _, m := range seen {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b module) int { return strings.Compare(strings.ToLower(a.path), strings.ToLower(b.path)) })
	return out, nil
}

func licenceFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && licenceFile.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

func section(b *bytes.Buffer, name, version, kind, dir string, files []string) error {
	fmt.Fprintf(b, "%s\n%s %s (%s)\n%s\n", strings.Repeat("=", 72), name, version, kind, strings.Repeat("-", 72))
	for i, f := range files {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return err
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.Write(bytes.TrimRight(raw, "\n\r\t "))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return nil
}
