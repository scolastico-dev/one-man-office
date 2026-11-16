package pluginmanager

// The plugin list omo provides: a YAML file in omo's repository
// (plugins/index.yaml), which the release workflow copies to the release
// branch together with the plugins it names. omo reads it from there with
// gitops.FetchSource, on demand, never at start.
//
// The file:
//
//	plugins:
//	  - name: pushover              # a plugin name (plugins.ValidName)
//	    tag: official               # official, recommended or community
//	    description: One line, at most 200 characters.
//	    path: plugins/pushover      # the plugin's folder in its repository
//	  - name: example
//	    tag: community
//	    description: ...
//	    source: https://example.org/omo-example.git
//	    ref: v1.2.0                 # optional: empty is the default branch
//	    path: ""                    # optional for others: the top folder
//
// An official plugin lives in the list's own repository: it has no source
// and no ref of its own and installs from the repository and branch the
// list was read from (so a fork or a test that points omo at another list
// gets its own official plugins). Every other plugin names its own
// repository. Everything in the file is data from the network: it is
// checked strictly (size, known keys, one document, tags, names, addresses
// through gitops.ValidSourceURL, folders through cleanSubpath, plain one-line
// descriptions) and the whole list is refused when one entry is wrong.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/buildinfo"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Where the list is and how large it may be.
const (
	// CatalogFile is the list's path in its repository.
	CatalogFile = "plugins/index.yaml"
	// DefaultCatalogRef is the branch the list is read from.
	DefaultCatalogRef = "release"
	// CatalogEnv overrides where the list is read from:
	// "<source>[#<ref>]", a source as plugins take it (an absolute folder for
	// tests) and an optional branch or tag (empty: the default branch).
	CatalogEnv = "OMO_PLUGIN_CATALOG"
	// MaxCatalogBytes bounds the list file.
	MaxCatalogBytes = 64 << 10
	// MaxCatalogPlugins bounds the number of plugins it names.
	MaxCatalogPlugins = 200
	// MaxCatalogDescription bounds a description, in characters.
	MaxCatalogDescription = 200
	// maxCatalogSource bounds a source address.
	maxCatalogSource = 2048
	// catalogFolder is the checkout of the list below Root, removed after
	// every read. A plugin name never starts with a dot.
	catalogFolder = ".catalog"
)

// DefaultCatalogSource is omo's own repository.
const DefaultCatalogSource = buildinfo.Repository + ".git"

// CatalogLocation is the repository and branch the list is read from. The
// zero value is no list.
type CatalogLocation struct {
	Source string
	Ref    string
}

// DefaultCatalog is the release branch of omo's repository.
func DefaultCatalog() CatalogLocation {
	return CatalogLocation{Source: DefaultCatalogSource, Ref: DefaultCatalogRef}
}

// CatalogFromEnv reads the value of CatalogEnv: "" is DefaultCatalog,
// otherwise "<source>" or "<source>#<ref>" (split at the last "#").
func CatalogFromEnv(v string) (CatalogLocation, error) {
	if v == "" {
		return DefaultCatalog(), nil
	}
	loc := CatalogLocation{Source: v}
	if i := strings.LastIndex(v, "#"); i >= 0 {
		loc = CatalogLocation{Source: v[:i], Ref: v[i+1:]}
	}
	if err := gitops.ValidSourceURL(loc.Source); err != nil {
		return CatalogLocation{}, fmt.Errorf("%s: %w", CatalogEnv, err)
	}
	if err := gitops.ValidRef(loc.Ref); err != nil {
		return CatalogLocation{}, fmt.Errorf("%s: %w", CatalogEnv, err)
	}
	return loc, nil
}

// Catalog is a list as read: where from, at which commit, its plugins with
// their sources resolved.
type Catalog struct {
	Location CatalogLocation
	Commit   string
	Plugins  []api.CatalogPlugin
}

// TagOf is the tag of the plugin at source, subpath and ref: the tag of the
// listed plugin with exactly that source, folder and ref, else community.
func (c Catalog) TagOf(source, subpath, ref string) string {
	sub, err := cleanSubpath(subpath)
	if err != nil {
		return api.PluginCommunity
	}
	for _, p := range c.Plugins {
		if p.Source == source && p.Subpath == sub && p.Ref == ref {
			return p.Tag
		}
	}
	return api.PluginCommunity
}

// Acknowledged reports whether the acknowledgement ack (the warning the user
// confirmed: "", recommended or community) covers a plugin tagged tag. An
// official plugin needs none; community covers recommended.
func Acknowledged(tag, ack string) bool {
	switch tag {
	case api.PluginOfficial:
		return true
	case api.PluginRecommended:
		return ack == api.PluginRecommended || ack == api.PluginCommunity
	}
	return ack == api.PluginCommunity
}

// ValidAcknowledgement reports whether ack is a value the install routes
// take.
func ValidAcknowledgement(ack string) bool {
	return ack == "" || ack == api.PluginRecommended || ack == api.PluginCommunity
}

// catalogFileShape is the file as it is decoded; unknown keys are refused.
type catalogFileShape struct {
	Plugins []catalogEntry `yaml:"plugins"`
}

type catalogEntry struct {
	Name        string `yaml:"name"`
	Tag         string `yaml:"tag"`
	Description string `yaml:"description"`
	Source      string `yaml:"source"`
	Ref         string `yaml:"ref"`
	Path        string `yaml:"path"`
}

// ParseCatalog reads a list file read from at. Official plugins get at's
// source and ref. Any error refuses the whole list; its message names the
// entry (by position) and the key, never a value.
func ParseCatalog(raw []byte, at CatalogLocation) ([]api.CatalogPlugin, error) {
	if len(raw) > MaxCatalogBytes {
		return nil, fmt.Errorf("the plugin list is larger than %s", homefile.Size(MaxCatalogBytes))
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("the plugin list is not UTF-8 text")
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var f catalogFileShape
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the plugin list is empty")
		}
		return nil, fmt.Errorf("the plugin list is not valid: %s", quote(oneLine(err.Error())))
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("the plugin list must be one YAML document")
	}
	if len(f.Plugins) > MaxCatalogPlugins {
		return nil, fmt.Errorf("the plugin list names more than %d plugins", MaxCatalogPlugins)
	}
	out := make([]api.CatalogPlugin, 0, len(f.Plugins))
	names := map[string]bool{}
	places := map[[3]string]bool{}
	for i, e := range f.Plugins {
		p, err := catalogPlugin(e, at)
		if err != nil {
			return nil, fmt.Errorf("plugin %d of the list: %w", i+1, err)
		}
		folded := strings.ToLower(p.Name)
		if names[folded] {
			return nil, fmt.Errorf("plugin %d of the list: the name %s is listed twice", i+1, quote(p.Name))
		}
		names[folded] = true
		place := [3]string{p.Source, p.Subpath, p.Ref}
		if places[place] {
			return nil, fmt.Errorf("plugin %d of the list: its source, folder and ref are listed twice", i+1)
		}
		places[place] = true
		out = append(out, p)
	}
	return out, nil
}

// catalogPlugin checks one entry and resolves its source.
func catalogPlugin(e catalogEntry, at CatalogLocation) (api.CatalogPlugin, error) {
	if !plugins.ValidName(e.Name) {
		return api.CatalogPlugin{}, errors.New("name: " + nameRule)
	}
	if err := checkDescription(e.Description); err != nil {
		return api.CatalogPlugin{}, err
	}
	sub, err := cleanSubpath(e.Path)
	if err != nil {
		return api.CatalogPlugin{}, errors.New("path must be a folder inside the repository: a relative path with / between folder names, without .. or .git")
	}
	p := api.CatalogPlugin{Name: e.Name, Description: e.Description, Tag: e.Tag, Subpath: sub}
	switch e.Tag {
	case api.PluginOfficial:
		if e.Source != "" || e.Ref != "" {
			return api.CatalogPlugin{}, errors.New("an official plugin has no source or ref: it is installed from the list's own repository and branch")
		}
		if sub == "" {
			return api.CatalogPlugin{}, errors.New("an official plugin needs its folder in path")
		}
		p.Source, p.Ref = at.Source, at.Ref
	case api.PluginRecommended, api.PluginCommunity:
		if len(e.Source) > maxCatalogSource || gitops.ValidSourceURL(e.Source) != nil {
			return api.CatalogPlugin{}, errors.New("source must be an https:// or ssh:// address or a host:path address")
		}
		if gitops.ValidRef(e.Ref) != nil {
			return api.CatalogPlugin{}, errors.New("ref must be empty, a branch or tag name or a full commit id")
		}
		p.Source, p.Ref = e.Source, e.Ref
	default:
		return api.CatalogPlugin{}, errors.New("tag must be official, recommended or community")
	}
	return p, nil
}

// checkDescription accepts one line of 1 to MaxCatalogDescription printable
// characters.
func checkDescription(d string) error {
	if strings.TrimSpace(d) == "" || utf8.RuneCountInString(d) > MaxCatalogDescription {
		return fmt.Errorf("description must be 1 to %d characters", MaxCatalogDescription)
	}
	for _, r := range d {
		if unicode.IsControl(r) || textsafe.IsHidden(r) {
			return errors.New("description must be one line without control, invisible or direction characters")
		}
	}
	return nil
}

// FetchCatalog fetches the repository at loc through gitops.FetchSource into
// Root/.catalog, reads and checks its CatalogFile and removes the checkout
// again. An absolute local folder is fetched like any other source.
func FetchCatalog(ctx context.Context, o Options, loc CatalogLocation) (Catalog, error) {
	if loc.Source == "" {
		return Catalog{}, errors.New("this omo has no plugin list")
	}
	if o.Git == nil {
		return Catalog{}, errors.New("plugin installs from git are not available")
	}
	root, err := rootOf(o)
	if err != nil {
		return Catalog{}, err
	}
	unlock, err := lock(ctx, root, catalogFolder)
	if err != nil {
		return Catalog{}, err
	}
	defer unlock()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Catalog{}, errors.New("the plugins folder cannot be created")
	}
	dir := filepath.Join(root, catalogFolder)
	defer os.RemoveAll(dir)
	commit, err := o.Git.FetchSource(ctx, loc.Source, loc.Ref, dir)
	if err != nil {
		return Catalog{}, err
	}
	raw, err := homefile.Read(filepath.Join(dir, filepath.FromSlash(CatalogFile)), MaxCatalogBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Catalog{}, fmt.Errorf("the repository has no %s", CatalogFile)
	}
	if err != nil {
		if text, ok := homefile.Refusal(err, MaxCatalogBytes); ok {
			return Catalog{}, fmt.Errorf("%s: %s", CatalogFile, text)
		}
		return Catalog{}, fmt.Errorf("%s cannot be read", CatalogFile)
	}
	list, err := ParseCatalog(raw, loc)
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{Location: loc, Commit: commit, Plugins: list}, nil
}
