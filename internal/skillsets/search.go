package skillsets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Searching skills.sh. omo asks the search of the official `skills` CLI
// (`npx skills find`): GET <base>/api/search?q=<text>, no key. Its answer is
//
//	{"query": "...", "searchType": "...", "skills": [
//	  {"id": "owner/repo/skill", "source": "owner/repo", "skillId": "skill",
//	   "name": "skill", "installs": 123}, ...]}
//
// Only the search text is sent (no other parameter, no cookie, a generic
// User-Agent). Every string of the answer is someone else's text: results
// whose source is not a GitHub repository (owner/repo) or whose skill id is
// not a plain name are left out, names are made one line and cut.
const (
	// SkillsShSite is where a result's page is, on skills.sh.
	SkillsShSite = "https://skills.sh"
	// MinSearch and MaxSearch bound the search text, in characters.
	MinSearch = 2
	MaxSearch = 100
	// MaxResults is the most results one search answers.
	MaxResults = 25
	// searchTimeout bounds one request to skills.sh.
	searchTimeout = 10 * time.Second
	// maxSearchAnswer is the most bytes of an answer that are read.
	maxSearchAnswer = 512 << 10
	// searchFresh is how long an answer is used again for the same text.
	searchFresh = time.Minute
	// maxCached is the most answers kept.
	maxCached = 100
	// maxResultName is the most characters of a result's name.
	maxResultName = 80
	// userAgent names omo and nothing about the machine or the user.
	userAgent = "omo"
)

var (
	// githubSource is owner/repo as GitHub allows them.
	githubSource = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
	// skillID is the skill's name in a result's id and page address.
	skillID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// ValidSource reports whether source is a GitHub repository as owner/repo.
func ValidSource(source string) bool {
	if !githubSource.MatchString(source) {
		return false
	}
	_, repo, _ := strings.Cut(source, "/")
	return repo != "." && repo != ".." && !strings.HasSuffix(strings.ToLower(repo), ".git")
}

// ValidSkillID reports whether id is a skill id omo takes from skills.sh.
func ValidSkillID(id string) bool {
	return skillID.MatchString(id)
}

// SourceRepository is the git address of a GitHub repository owner/repo.
func SourceRepository(source string) string {
	return "https://github.com/" + source + ".git"
}

// ErrSearch marks a search that skills.sh did not answer usefully.
var ErrSearch = errors.New("skills.sh could not be reached")

// SearchOptions configure a Searcher.
type SearchOptions struct {
	// Base is the address of skills.sh ("" is https://skills.sh); tests
	// point it at their own server.
	Base string
	// Client makes the requests; nil is a client of its own.
	Client *http.Client
	// Timeout bounds one request (0: 10 seconds).
	Timeout time.Duration
	// Now is the clock of the cache (nil: time.Now).
	Now func() time.Time
}

// Searcher searches skills.sh and keeps recent answers for a minute.
type Searcher struct {
	o     SearchOptions
	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at      time.Time
	results []api.SkillSearchResult
}

// NewSearcher makes a Searcher.
func NewSearcher(o SearchOptions) *Searcher {
	if o.Base == "" {
		o.Base = SkillsShSite
	}
	o.Base = strings.TrimRight(o.Base, "/")
	if o.Client == nil {
		o.Client = &http.Client{}
	}
	if o.Timeout <= 0 {
		o.Timeout = searchTimeout
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Searcher{o: o, cache: map[string]cached{}}
}

// SearchText is q as it is sent: one line, cleaned of hidden characters;
// an error names a text that is too short or too long.
func SearchText(q string) (string, error) {
	q = oneLine(q)
	switch n := len([]rune(q)); {
	case n < MinSearch:
		return "", fmt.Errorf("type at least %d characters", MinSearch)
	case n > MaxSearch:
		return "", fmt.Errorf("the search text is longer than %d characters", MaxSearch)
	}
	return q, nil
}

// Search answers the skills skills.sh finds for q (see SearchText), at most
// MaxResults, most installed first as skills.sh orders them. An error wraps
// ErrSearch and says why in words.
func (s *Searcher) Search(ctx context.Context, q string) ([]api.SkillSearchResult, error) {
	q, err := SearchText(q)
	if err != nil {
		return nil, err
	}
	key := strings.ToLower(q)
	now := s.o.Now()
	s.mu.Lock()
	if c, ok := s.cache[key]; ok && now.Sub(c.at) < searchFresh {
		s.mu.Unlock()
		return c.results, nil
	}
	s.mu.Unlock()

	results, err := s.ask(ctx, q)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, c := range s.cache {
		if now.Sub(c.at) >= searchFresh {
			delete(s.cache, k)
		}
	}
	if len(s.cache) >= maxCached {
		clear(s.cache)
	}
	s.cache[key] = cached{at: now, results: results}
	return results, nil
}

// ask sends q to skills.sh and reads the answer.
func (s *Searcher) ask(ctx context.Context, q string) ([]api.SkillSearchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.o.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.o.Base+"/api/search?"+url.Values{"q": {q}}.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSearch, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	res, err := s.o.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: no answer within %s", ErrSearch, s.o.Timeout)
		}
		return nil, fmt.Errorf("%w: the connection failed", ErrSearch)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: it answered %d %s", ErrSearch, res.StatusCode, http.StatusText(res.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxSearchAnswer+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: no answer within %s", ErrSearch, s.o.Timeout)
		}
		return nil, fmt.Errorf("%w: the answer broke off", ErrSearch)
	}
	if len(data) > maxSearchAnswer {
		return nil, fmt.Errorf("%w: the answer is larger than %d KiB", ErrSearch, maxSearchAnswer>>10)
	}
	return parseSearch(data)
}

// searchAnswer is what omo reads of the answer.
type searchAnswer struct {
	Skills *[]struct {
		Source   string  `json:"source"`
		SkillID  string  `json:"skillId"`
		Name     string  `json:"name"`
		Installs float64 `json:"installs"`
	} `json:"skills"`
}

// parseSearch reads an answer of skills.sh into results.
func parseSearch(data []byte) ([]api.SkillSearchResult, error) {
	var a searchAnswer
	if err := json.Unmarshal(data, &a); err != nil || a.Skills == nil {
		return nil, fmt.Errorf("%w: it gave an answer omo cannot read", ErrSearch)
	}
	out := []api.SkillSearchResult{}
	seen := map[string]bool{}
	for _, k := range *a.Skills {
		if len(out) >= MaxResults {
			break
		}
		if !ValidSource(k.Source) || !ValidSkillID(k.SkillID) {
			continue
		}
		id := k.Source + "/" + k.SkillID
		if seen[strings.ToLower(id)] {
			continue
		}
		seen[strings.ToLower(id)] = true
		name := cut(oneLine(k.Name), maxResultName)
		if name == "" {
			name = k.SkillID
		}
		installs := int64(0)
		if k.Installs > 0 && k.Installs < 1e15 {
			installs = int64(k.Installs)
		}
		out = append(out, api.SkillSearchResult{
			ID:         id,
			Name:       name,
			SkillID:    k.SkillID,
			Source:     k.Source,
			Installs:   installs,
			Repository: SourceRepository(k.Source),
			URL:        SkillsShSite + "/" + id,
		})
	}
	return out, nil
}
