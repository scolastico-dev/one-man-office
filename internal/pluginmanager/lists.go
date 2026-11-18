package pluginmanager

// Additional plugin lists: files in the format of omo's own list
// (plugins/index.yaml) that the user names by http:// or https:// address
// in settings.yaml (plugin_lists), for example a raw.githubusercontent.com
// address of a file in someone's repository. omo fetches the file as it is
// (no git), bounded in time, size and redirects, and shows its plugins
// beside its own list.
//
// Such a list is someone else's word, so it vouches for nothing: every
// plugin of it is a community plugin (TrustTag), whatever tag the file
// gives, and an install of one needs the user's acknowledgement like any
// source omo's own list does not name. A list cannot stand in for omo's
// own plugins either: a plugin whose name or source, folder and ref omo's
// own list (or an earlier additional list) already has is left out with a
// note (Combine). As a list has no repository of its own, every entry
// needs its full source; the official form (only a path inside the list's
// repository) does not work here. One bad entry is left out with a note
// naming it; a file that is not a plugin list at all is refused whole.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

// Bounds of fetching an additional list. Its size is bounded by
// MaxCatalogBytes like omo's own list.
const (
	// ListTimeout bounds one fetch, redirects included.
	ListTimeout = 15 * time.Second
	// maxListRedirects is how many redirects a fetch follows.
	maxListRedirects = 5
	// listUserAgent is what omo calls itself when it fetches a list: no
	// version, no host, nothing about the user.
	listUserAgent = "omo-plugin-list"
)

// List is an additional plugin list as read: its address, its host, the
// plugins it names (each marked with the list and tagged community) and
// notes on the entries left out.
type List struct {
	URL     string
	Origin  string
	Plugins []api.CatalogPlugin
	Notes   []string
}

// ListOrigin is the host (with its port, if any) of a list's address; ""
// when the address cannot be read.
func ListOrigin(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return u.Host
}

// listClient fetches lists: redirects bounded, only to http:// and
// https://.
var listClient = &http.Client{
	Transport: http.DefaultTransport.(*http.Transport).Clone(),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Referer")
		if len(via) > maxListRedirects {
			return fmt.Errorf("the address answered with more than %d redirects", maxListRedirects)
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("the address redirected to an address that is not http:// or https://")
		}
		return nil
	},
}

// checkListAddress accepts an absolute http:// or https:// address with a
// host and without user name or password (the settings check the rest).
func checkListAddress(address string) error {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return errors.New("a plugin list is fetched only from an http:// or https:// address")
	}
	if u.User != nil {
		return errors.New("the address of a plugin list must not hold a user name or password")
	}
	return nil
}

// FetchList fetches the list file at address (http or https only), bounded
// to ListTimeout (or ctx's earlier deadline), MaxCatalogBytes and
// maxListRedirects, and reads it with ParseList. Errors never repeat the
// address or the server's answer.
func FetchList(ctx context.Context, address string) (List, error) {
	if err := checkListAddress(address); err != nil {
		return List{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, ListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return List{}, errors.New("the address cannot be fetched")
	}
	req.Header.Set("User-Agent", listUserAgent)
	req.Header.Set("Accept", "application/yaml, text/yaml, text/plain;q=0.9, */*;q=0.1")
	resp, err := listClient.Do(req)
	if err != nil {
		return List{}, fetchError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return List{}, fmt.Errorf("the address answered %s", oneLine(resp.Status))
	}
	tooLarge := fmt.Errorf("the plugin list is larger than %s", homefile.Size(MaxCatalogBytes))
	if resp.ContentLength > MaxCatalogBytes {
		return List{}, tooLarge
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxCatalogBytes+1))
	if err != nil {
		return List{}, fetchError(ctx, err)
	}
	if len(raw) > MaxCatalogBytes {
		return List{}, tooLarge
	}
	return ParseList(raw, address)
}

// fetchError says why a fetch failed without the address (a *url.Error
// carries it, query and all).
func fetchError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("fetching the list took too long (more than %s)", ListTimeout)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("the list could not be fetched: %s", quote(oneLine(err.Error())))
}

// ParseList reads an additional list file fetched from address. The file
// as a whole is checked as omo's own list is (ParseCatalog); each entry is
// checked by listPlugin, and an entry that fails, or repeats an earlier
// entry's name (in any case) or source, folder and ref, is left out with a
// note.
func ParseList(raw []byte, address string) (List, error) {
	entries, err := decodeCatalog(raw)
	if err != nil {
		return List{}, err
	}
	l := List{URL: address, Origin: ListOrigin(address), Plugins: []api.CatalogPlugin{}, Notes: []string{}}
	names := map[string]bool{}
	places := map[[3]string]bool{}
	for i, e := range entries {
		p, err := listPlugin(e)
		if err == nil && names[strings.ToLower(p.Name)] {
			err = fmt.Errorf("the name %s is listed twice", quote(p.Name))
		} else if err == nil && places[placeOf(p)] {
			err = errors.New("its source, folder and ref are listed twice")
		}
		if err != nil {
			l.Notes = append(l.Notes, fmt.Sprintf("plugin %d of the list is not shown: %s", i+1, err))
			continue
		}
		names[strings.ToLower(p.Name)] = true
		places[placeOf(p)] = true
		p.List, p.Origin = address, l.Origin
		l.Plugins = append(l.Plugins, p)
	}
	return l, nil
}

// listPlugin checks one entry of an additional list: the same rules as an
// entry of omo's own list (name, description, path, a valid tag), but the
// source is always required, and the tag is always community.
func listPlugin(e catalogEntry) (api.CatalogPlugin, error) {
	if !plugins.ValidName(e.Name) {
		return api.CatalogPlugin{}, errors.New("name: " + nameRule)
	}
	if err := checkDescription(e.Description); err != nil {
		return api.CatalogPlugin{}, err
	}
	switch e.Tag {
	case api.PluginOfficial, api.PluginRecommended, api.PluginCommunity:
	default:
		return api.CatalogPlugin{}, errors.New("tag must be official, recommended or community")
	}
	sub, err := cleanSubpath(e.Path)
	if err != nil {
		return api.CatalogPlugin{}, errors.New("path must be a folder inside the repository: a relative path with / between folder names, without .. or .git")
	}
	if e.Source == "" {
		return api.CatalogPlugin{}, errors.New("source is missing: a list read from an address has no repository of its own, so every plugin needs its full source address")
	}
	if len(e.Source) > maxCatalogSource || gitops.ValidSourceURL(e.Source) != nil {
		return api.CatalogPlugin{}, errors.New("source must be an https:// or ssh:// address or a host:path address")
	}
	if gitops.ValidRef(e.Ref) != nil {
		return api.CatalogPlugin{}, errors.New("ref must be empty, a branch or tag name or a full commit id")
	}
	return api.CatalogPlugin{Name: e.Name, Description: e.Description, Tag: api.PluginCommunity, Source: e.Source, Ref: e.Ref, Subpath: sub}, nil
}

// Combine answers the plugins of omo's own list followed by those of the
// additional lists in their order, and per list the notes on its plugins
// left out: one whose name (in any case), or whose source, folder and ref,
// omo's own list or an earlier additional list has. omo's own plugins are
// never replaced. The lists given are not changed.
func Combine(own []api.CatalogPlugin, lists []List) ([]api.CatalogPlugin, [][]string) {
	out := slices.Clone(own)
	const omo = "omo's own list"
	names := map[string]string{}
	places := map[[3]string]string{}
	for _, p := range own {
		names[strings.ToLower(p.Name)] = omo
		places[placeOf(p)] = omo
	}
	notes := make([][]string, len(lists))
	for i, l := range lists {
		notes[i] = []string{}
		by := "the list from " + l.Origin
		for _, p := range l.Plugins {
			if who, ok := names[strings.ToLower(p.Name)]; ok {
				notes[i] = append(notes[i], fmt.Sprintf("%s is not shown: %s has a plugin of this name", quote(p.Name), who))
				continue
			}
			if who, ok := places[placeOf(p)]; ok {
				notes[i] = append(notes[i], fmt.Sprintf("%s is not shown: %s has a plugin with the same source, folder and ref", quote(p.Name), who))
				continue
			}
			names[strings.ToLower(p.Name)] = by
			places[placeOf(p)] = by
			out = append(out, p)
		}
	}
	return out, notes
}
