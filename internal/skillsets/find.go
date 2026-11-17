package skillsets

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// How a set's paths become skills (Find):
//
//   - no paths: the whole checkout, as a folder;
//   - a folder with a SKILL.md: that one skill;
//   - a folder without one: every folder below it that has a SKILL.md
//     (not looking further into a skill's own folder), up to maxDepth
//     levels down;
//   - a file named SKILL.md: the skill of its folder;
//   - any other file: a skill of its own, named after the file without
//     its extension (its front matter may name and describe it).
//
// Folders and files whose names start with a dot are not looked into, and
// links are never followed (a linked path counts as missing).
const (
	skillFile = "SKILL.md"
	// maxDepth is how many folder levels below a selected folder are
	// searched for skills.
	maxDepth = 8
	// maxSkills is the most skills one set lists.
	maxSkills = 100
	// maxHead is how much of a skill's file is read for its front matter.
	maxHead = 8 << 10
	// maxDescription is the most characters of a description that are kept.
	maxDescription = 300
)

// skillName is a name a skill's front matter may give it; any other name
// is replaced by the name of its folder or file.
var skillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._:-]{0,63}$`)

// Skill is one skill of a checkout.
type Skill struct {
	// Name comes from the front matter, else from the folder or file.
	Name string
	// Description is the front matter's, one line, cut; may be "".
	Description string
	// OnRequest is set when the front matter says the skill is only used
	// when someone asks for it (disable-model-invocation).
	OnRequest bool
	// File is the absolute path of the file an agent reads.
	File string
}

// Find returns the skills the paths select in the checkout folder root, in
// the order of the paths (a folder's skills sorted by path), each file
// once, at most maxSkills. Paths the checkout does not have are skipped.
func Find(root string, paths []string) []Skill {
	if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
		return nil
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	var out []Skill
	seen := map[string]bool{}
	add := func(file, fallback string) {
		if seen[file] || len(out) >= maxSkills {
			return
		}
		seen[file] = true
		out = append(out, readSkill(file, fallback))
	}
	for _, p := range paths {
		full, kind := entry(root, p)
		switch kind {
		case kindDir:
			walk(full, 0, func(dir string) { add(filepath.Join(dir, skillFile), filepath.Base(dir)) })
		case kindFile:
			if filepath.Base(full) == skillFile {
				add(full, filepath.Base(filepath.Dir(full)))
			} else {
				add(full, strings.TrimSuffix(filepath.Base(full), filepath.Ext(full)))
			}
		}
	}
	return out
}

// Missing returns the paths the checkout folder root does not have as a
// folder or a regular file (a link counts as missing), in their order;
// nil when there is no checkout.
func Missing(root string, paths []string) []string {
	if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
		return nil
	}
	var out []string
	for _, p := range paths {
		if _, kind := entry(root, p); kind == kindNone {
			out = append(out, p)
		}
	}
	return out
}

type entryKind int

const (
	kindNone entryKind = iota
	kindDir
	kindFile
)

// entry resolves the path p of a set below root without following links:
// every part of it must be a real folder, the last a folder or a regular
// file. A path that leaves root is none.
func entry(root, p string) (string, entryKind) {
	full := root
	if p == "" {
		return full, kindDir
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return "", kindNone
		}
		full = filepath.Join(full, part)
		fi, err := os.Lstat(full)
		if err != nil {
			return "", kindNone
		}
		switch {
		case fi.IsDir():
		case fi.Mode().IsRegular() && full == filepath.Join(root, filepath.FromSlash(p)):
			return full, kindFile
		default:
			return "", kindNone
		}
	}
	return full, kindDir
}

// walk calls skill for dir when it holds a SKILL.md, else for every folder
// below it that does, depth levels deep at most, sorted by name.
func walk(dir string, depth int, skill func(dir string)) {
	if fi, err := os.Lstat(filepath.Join(dir, skillFile)); err == nil && fi.Mode().IsRegular() {
		skill(dir)
		return
	}
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&os.ModeType == os.ModeDir && !strings.HasPrefix(e.Name(), ".") {
			walk(filepath.Join(dir, e.Name()), depth+1, skill)
		}
	}
}

// frontMatter is what omo reads of a skill's front matter.
type frontMatter struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
}

// readSkill reads the front matter of file (a regular file) and makes the
// skill; fallback names it when the front matter gives no usable name.
func readSkill(file, fallback string) Skill {
	s := Skill{Name: oneLine(fallback), File: file}
	var fm frontMatter
	if head := readHead(file); head != nil {
		_ = yaml.Unmarshal(head, &fm)
	}
	if name := oneLine(fm.Name); skillName.MatchString(name) && name == strings.TrimSpace(fm.Name) {
		s.Name = name
	}
	s.Description = cut(oneLine(fm.Description), maxDescription)
	s.OnRequest = fm.DisableModelInvocation
	return s
}

// readHead returns the front matter of file: the lines between a first
// line "---" and the next "---" line within its first maxHead bytes; nil
// when there is none.
func readHead(file string) []byte {
	f, err := homefile.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxHead))
	if err != nil {
		return nil
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return nil
	}
	data = data[4:]
	end := bytes.Index(data, []byte("\n---"))
	if end < 0 {
		return nil
	}
	return data[:end+1]
}

// oneLine is s cleaned of hidden characters with every run of white space
// (line breaks too) made one space.
func oneLine(s string) string {
	return strings.Join(strings.Fields(clean(s)), " ")
}

// cut shortens s to n characters, the last one an ellipsis.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
