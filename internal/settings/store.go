// Package settings keeps the settings folders of the omo home: the general
// settings file and one file per project, profile, role and prompt. The files
// are the source of truth; hand edits are picked up by polling, and a file
// that cannot be loaded is reported while its last valid content stays in
// effect.
package settings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"gopkg.in/yaml.v3"
)

// Sections named by Change.
const (
	SectionGeneral  = api.SectionGeneral
	SectionProjects = "projects"
	SectionProfiles = api.SectionProfiles
	SectionRoles    = api.SectionRoles
	SectionPrompts  = api.SectionPrompts
	// SectionUsage names the usage scripts (usage/<name>.lua).
	SectionUsage = api.SectionUsage
	// SectionSkillSets names the skill sets (skillsets/<name>.yaml).
	SectionSkillSets = api.SectionSkillSets
	// SectionPlugins names the plugin settings (plugins/<name>.yaml).
	SectionPlugins = api.SectionPlugins
)

// FileError reports a settings file that could not be loaded.
type FileError = api.FileError

// Options configures a Store.
type Options struct {
	// PromptDefaults maps prompt names to their embedded default text.
	PromptDefaults map[string]string
	// OnChange is called after every change, whether it came from a store
	// method or from a hand edit found by Reload. Calls are serialized and
	// happen without the store's state lock held, so the callback may read
	// the store; it must not modify it.
	OnChange func(Change)
	// PollInterval is how often Run calls Reload; zero means 2s.
	PollInterval time.Duration
	// ReservedProjectIDs returns ids that tickets reference; it is called
	// without the store's locks, before a project is added. nil: none.
	ReservedProjectIDs func() map[string]bool
	// ValidatePrompt checks the text of a prompt before it is saved; its
	// error is the refusal's message. It is called without the store's
	// locks. nil: no check.
	ValidatePrompt func(name, text string) error
	// UsageDefaults maps usage script names to their embedded default
	// script. nil: no defaults, and a profile's usage_script is not checked
	// against the scripts on write (a store that knows no scripts at all).
	UsageDefaults map[string]string
	// ValidateUsageScript checks a usage script before it is saved (for
	// example that it compiles and defines usage); its error is the
	// refusal's message. It is called without the store's locks. nil: no
	// check.
	ValidateUsageScript func(name, text string) error
}

// Change describes one changed settings item. Name is the item's id (empty
// for the general settings). Removed is set when the item is gone. Err is set
// when the item's file could not be loaded; the item's last valid content
// stays in effect. FromDisk is set for a change found on disk (a hand edit,
// found by Reload or by the re-read before a write) and unset for the store's
// own writes.
type Change struct {
	Section  string
	Name     string
	Removed  bool
	Err      *FileError
	FromDisk bool
}

// idPattern is the pattern every item name (and so every file base name)
// must match.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// kind describes one settings section stored on disk: either a single file or
// a folder with one file per item.
type kind struct {
	section string
	file    string // single-file section: its path
	dir     string // folder section: its path
	ext     string // folder section: item file extension
	// names is the pattern of item names of a folder section; nil means
	// idPattern.
	names *regexp.Regexp
	// skipDirs makes a folder section pass over sub-folders: they hold
	// something else than settings (plugin trees, caches), whatever their
	// name.
	skipDirs bool
	limit    int64 // the largest file of the section, in bytes
	// load parses data, validates it and, only when valid, makes it the
	// item's content. It runs with the state lock held.
	load func(name string, data []byte) error
	// drop forgets an item whose file vanished and reports whether there
	// was a loaded item. It runs with the state lock held.
	drop func(name string) bool
}

// namePattern is the pattern item names of the section must match.
func (k *kind) namePattern() *regexp.Regexp {
	if k.names != nil {
		return k.names
	}
	return idPattern
}

// fileState is what the store last saw of one file. sum is the hash of its
// bytes when read is set; a file that could not be read has only err.
type fileState struct {
	size  int64
	mtime time.Time
	sum   [32]byte
	read  bool
	err   *FileError
}

// Store holds the loaded settings. All methods are safe for concurrent use.
type Store struct {
	root  string
	opts  Options
	kinds []*kind

	// opMu serializes every operation that changes state and the delivery
	// of its changes, so OnChange sees changes in order.
	opMu sync.Mutex
	// mu guards the fields below.
	mu       sync.RWMutex
	files    map[string]*fileState // by absolute path
	general  General
	projects map[string]Project // by id
	profiles map[string]Profile // by name
	roles    map[string]Role    // by name, always all of RoleNames
	prompts  map[string]string  // override text by name
	scripts  map[string]string  // usage script files by name
	skills   map[string]SkillSet
	plugins  map[string]PluginSettings
}

// Open loads the settings below root (the omo home). It creates the settings
// folders when missing and writes settings.yaml with the defaults when it is
// absent. Files that cannot be loaded become FileErrors; Open itself fails
// only when the folders cannot be created or read. Open does not call
// OnChange.
func Open(root string, o Options) (*Store, error) {
	if o.PollInterval <= 0 {
		o.PollInterval = 2 * time.Second
	}
	s := &Store{
		root:     root,
		opts:     o,
		files:    map[string]*fileState{},
		general:  Defaults(),
		projects: map[string]Project{},
		profiles: map[string]Profile{},
		roles:    map[string]Role{},
		prompts:  map[string]string{},
		scripts:  map[string]string{},
		skills:   map[string]SkillSet{},
		plugins:  map[string]PluginSettings{},
	}
	s.opts.UsageDefaults = maps.Clone(o.UsageDefaults)
	for _, name := range RoleNames {
		s.roles[name] = emptyRole(name)
	}
	s.kinds = []*kind{s.generalKind(), s.projectKind(), s.profileKind(), s.roleKind(), s.promptKind(),
		s.usageKind(), s.skillSetKind(), s.pluginKind()}
	for _, k := range s.kinds {
		dir := k.dir
		if dir == "" {
			dir = filepath.Dir(k.file)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err := s.writeDefaultGeneral(); err != nil {
		return nil, err
	}
	if err := s.writeDefaultRoles(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.scan(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload re-reads every settings file that appeared, vanished or changed
// size or modification time since the last look, calls OnChange for each
// resulting change and returns the changes.
func (s *Store) Reload() []Change {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	changes, _ := s.scan()
	s.mu.Unlock()
	for i := range changes {
		changes[i].FromDisk = true
	}
	s.notify(changes)
	return changes
}

// Run calls Reload every PollInterval until ctx ends.
func (s *Store) Run(ctx context.Context) {
	t := time.NewTicker(s.opts.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Reload()
		}
	}
}

// Errors lists the files that could not be loaded, sorted by file.
func (s *Store) Errors() []FileError {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []FileError{}
	for _, f := range s.files {
		if f.err != nil {
			out = append(out, *f.err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// mutate runs fn with the state lock held and delivers the changes it
// returns once the lock is released, also when fn fails.
func (s *Store) mutate(fn func() ([]Change, error)) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	changes, err := fn()
	s.mu.Unlock()
	s.notify(changes)
	return err
}

func (s *Store) notify(changes []Change) {
	if s.opts.OnChange == nil {
		return
	}
	for _, c := range changes {
		s.opts.OnChange(c)
	}
}

// scan compares every settings file with what was last seen and loads what
// changed. It runs with the state lock held.
func (s *Store) scan() ([]Change, error) {
	var changes []Change
	seen := map[string]bool{}
	for _, k := range s.kinds {
		if k.dir == "" {
			seen[k.file] = true
			if c, ok := s.check(k, "", k.file, false); ok {
				changes = append(changes, c)
			}
			continue
		}
		entries, err := os.ReadDir(k.dir)
		if err != nil && !os.IsNotExist(err) {
			return changes, err
		}
		for _, e := range entries {
			name := e.Name()
			// A folder with an item's name is reported, not skipped: it
			// is no item file. Sections that keep other things in
			// sub-folders skip every folder.
			if ignoredFile(name) || (k.skipDirs && e.IsDir()) {
				continue
			}
			path := filepath.Join(k.dir, name)
			if !strings.HasSuffix(name, k.ext) {
				if alt := altExt(k.ext); alt != "" && strings.HasSuffix(name, alt) {
					seen[path] = true
					if c, ok := s.fail(k, strings.TrimSuffix(name, alt), path, fmt.Sprintf("settings files must end in %s", k.ext)); ok {
						changes = append(changes, c)
					}
				}
				continue
			}
			seen[path] = true
			if c, ok := s.check(k, strings.TrimSuffix(name, k.ext), path, false); ok {
				changes = append(changes, c)
			}
		}
		// Files of this folder that vanished.
		for path, f := range s.files {
			if filepath.Dir(path) != k.dir || seen[path] {
				continue
			}
			delete(s.files, path)
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			dropped := strings.HasSuffix(path, k.ext) && k.drop(name)
			if dropped || f.err != nil {
				changes = append(changes, Change{Section: k.section, Name: name, Removed: true})
			}
		}
	}
	return changes, nil
}

// check loads one file if it changed and reports the resulting change. It
// skips a file whose size and modification time are unchanged unless force
// is set; then the bytes decide. A link, anything but a regular file and a
// file above the section's limit is reported broken without being read (see
// homefile), so neither the poll nor the re-read before a write waits on it.
func (s *Store) check(k *kind, name, path string, force bool) (Change, bool) {
	prev := s.files[path]
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if prev == nil {
			return Change{}, false
		}
		// Only single-file sections reach this; folder files are handled
		// by the vanished-file pass.
		delete(s.files, path)
		k.drop(name)
		return Change{Section: k.section, Name: name, Removed: true}, true
	}
	if err != nil {
		return s.fail(k, name, path, err.Error())
	}
	if msg, refused := refusedFile(info, k.limit); refused {
		return s.fail(k, name, path, msg)
	}
	if !force && prev != nil && prev.size == info.Size() && prev.mtime.Equal(info.ModTime()) {
		return Change{}, false
	}
	data, err := homefile.Read(path, k.limit)
	if err != nil {
		if msg, refused := homefile.Refusal(err, k.limit); refused {
			return s.fail(k, name, path, msg)
		}
		return s.fail(k, name, path, err.Error())
	}
	sum := sha256.Sum256(data)
	// Unchanged bytes. A file that failed to load is loaded again when its
	// file was touched, since the result can depend on other files; the
	// re-read before a write does not repeat its error.
	if prev != nil && prev.read && prev.sum == sum && (prev.err == nil || force) {
		prev.size, prev.mtime = info.Size(), info.ModTime()
		return Change{}, false
	}
	st := &fileState{size: info.Size(), mtime: info.ModTime(), sum: sum, read: true}
	s.files[path] = st
	if k.dir != "" && !k.namePattern().MatchString(name) {
		st.err = &FileError{File: path, Message: fmt.Sprintf("file name %q must match %s", name+k.ext, k.namePattern())}
	} else if err := k.load(name, data); err != nil {
		st.err = &FileError{File: path, Message: err.Error()}
	}
	if st.err != nil {
		e := *st.err
		return Change{Section: k.section, Name: name, Err: &e}, true
	}
	return Change{Section: k.section, Name: name}, true
}

// fail records an error for a file that could not be read, reporting it
// only when the error is new.
func (s *Store) fail(k *kind, name, path, msg string) (Change, bool) {
	prev := s.files[path]
	if prev != nil && prev.err != nil && prev.err.Message == msg {
		return Change{}, false
	}
	fe := &FileError{File: path, Message: msg}
	s.files[path] = &fileState{err: fe, mtime: time.Time{}, size: -1}
	e := *fe
	return Change{Section: k.section, Name: name, Err: &e}, true
}

// refusedFile is the fixed message for a file the store does not read, as
// Lstat describes it: a link, anything but a regular file, or a file above
// limit.
func refusedFile(info os.FileInfo, limit int64) (string, bool) {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return homefile.Refusal(homefile.ErrLink, limit)
	case !info.Mode().IsRegular():
		return homefile.Refusal(homefile.ErrNotRegular, limit)
	case info.Size() > limit:
		return homefile.Refusal(homefile.ErrTooLarge, limit)
	}
	return "", false
}

// written records a file the store wrote itself, so Reload does not report
// it again and any earlier error for it is cleared.
func (s *Store) written(path string, data []byte) {
	st := &fileState{sum: sha256.Sum256(data), size: -1, read: true}
	if info, err := os.Lstat(path); err == nil {
		st.size, st.mtime = info.Size(), info.ModTime()
	}
	s.files[path] = st
}

// writeFile writes an item file atomically and records it.
func (s *Store) writeFile(path string, data []byte) error {
	if err := globalhome.WriteFileAtomic(path, data, 0o600); err != nil {
		return api.Errorf(api.CodeInternal, "write %s: %v", path, err)
	}
	s.written(path, data)
	return nil
}

// removeFile deletes an item file and forgets it.
func (s *Store) removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return api.Errorf(api.CodeInternal, "remove %s: %v", path, err)
	}
	delete(s.files, path)
	return nil
}

// fileExists reports whether an item file is on disk, loaded or not.
func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// ignoredFile skips hidden files (including the store's own temporary files)
// and editor backups.
func ignoredFile(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~")
}

// altExt is the extension users commonly write by mistake for ext.
func altExt(ext string) string {
	if ext == ".yaml" {
		return ".yml"
	}
	return ""
}

// decodeYAML decodes data strictly into out: unknown keys are errors. An
// empty file leaves out unchanged.
func decodeYAML(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return yamlError(err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("a settings file must hold a single YAML document")
	}
	return nil
}

// yamlLine finds the line number in a message of the YAML decoder.
var yamlLine = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)

// yamlUnknownKey finds the key of an unknown-key message of the YAML decoder.
var yamlUnknownKey = regexp.MustCompile(`^field (.+) not found in type \S+$`)

// yamlSyntaxPhrase is what a syntax message of the YAML decoder may hold to be
// passed on as it is: plain words, no quoted text and no digits.
var yamlSyntaxPhrase = regexp.MustCompile(`^[a-zA-Z ,':-]{1,120}$`)

// yamlError turns an error of the YAML decoder into "line N: <kind of
// error>". The decoder quotes the offending value in some messages, and a
// value may be a secret: the result names lines and keys, never values.
func yamlError(err error) error {
	var te *yaml.TypeError
	if errors.As(err, &te) {
		parts := make([]string, 0, len(te.Errors))
		for _, msg := range te.Errors {
			parts = append(parts, yamlTypeMessage(msg))
		}
		if len(parts) > 3 {
			parts = append(parts[:3], fmt.Sprintf("and %d more", len(parts)-3))
		}
		return errors.New(strings.Join(parts, "; "))
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "yaml: ") {
		// An error of a value's own parser (a duration, for example), which
		// quotes the value.
		return errors.New("a value has the wrong format")
	}
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		if yamlSyntaxPhrase.MatchString(m[2]) {
			return fmt.Errorf("line %s: %s", m[1], m[2])
		}
		return fmt.Errorf("line %s: the file is not valid YAML", m[1])
	}
	if rest := strings.TrimPrefix(msg, "yaml: "); yamlSyntaxPhrase.MatchString(rest) {
		return errors.New(rest)
	}
	return errors.New("the file is not valid YAML")
}

// yamlTypeMessage maps one message of a yaml.TypeError to its line and a
// fixed phrase.
func yamlTypeMessage(msg string) string {
	line, rest := "", msg
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		line, rest = "line "+m[1]+": ", m[2]
	}
	switch {
	case yamlUnknownKey.MatchString(rest):
		return line + "unknown key " + showKey(yamlUnknownKey.FindStringSubmatch(rest)[1])
	case strings.Contains(rest, "already defined"), strings.Contains(rest, "already set"):
		return line + "a key appears twice"
	default:
		return line + "a value has the wrong type"
	}
}

// encodeYAML encodes v with two-space indentation.
func encodeYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExpandPath replaces a leading "~" with the user's home folder.
func ExpandPath(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}
