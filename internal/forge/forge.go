// Package forge knows the facts of a project's remote that an export needs:
// which forge hosts it, the repository's path and web address, and the one
// command line an export job runs to open a pull request there. omo never
// runs a forge's command line tool itself; it only looks it up on PATH.
//
// The command line passes every value omo knows as one single-quoted
// --flag=value argument, after checking the value holds no quote, line break
// or hidden character. The title and body come from the files title.txt and
// body.md that omo wrote into the job's folder, through "$(cat file)" in
// double quotes (or a file argument), so their text is one argument whatever
// it holds and is never read by the shell as code or as an option.
package forge

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Kinds of forge.
const (
	GitHub = "github"
	GitLab = "gitlab"
	Gitea  = "gitea" // Gitea and Forgejo, through tea
)

// Remote is a project's remote as a forge sees it.
type Remote struct {
	// Host is the remote's host name in lower case, without port; Repo the
	// repository's path (owner/name; more segments for GitLab groups).
	Host, Repo string
	// Web is https://<host>/<repo>, never with credentials; "" for a local
	// path.
	Web string
	// Kind is GitHub, GitLab, Gitea or "" (a local path).
	Kind string
}

// ErrNotARemote marks an address omo does not read as a forge's remote.
var ErrNotARemote = errors.New("not a remote address omo knows")

var (
	hostName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*$`)
	repoPath = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)+$`)
	userName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// maxHost is the longest host name.
const maxHost = 253

// Parse reads a remote address: https://, ssh:// or scp-like
// [user@]host:path, with or without .git; an absolute local path gives the
// zero Remote. Credentials are dropped. The host is GitHub for github.com,
// GitLab when its first label is "gitlab", else Gitea. Anything else (other
// schemes, relative paths, query or fragment, IP literals in brackets, a
// repository path that does not match ^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$
// or holds "." or ".." segments) is ErrNotARemote.
func Parse(raw string) (Remote, error) {
	if raw == "" || !utf8.ValidString(raw) || strings.ContainsFunc(raw, textsafe.IsHidden) || strings.ContainsAny(raw, " \\") {
		return Remote{}, fmt.Errorf("%w: it is empty or holds spaces or hidden characters", ErrNotARemote)
	}
	if strings.HasPrefix(raw, "/") {
		return Remote{}, nil
	}
	var host, port, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Remote{}, fmt.Errorf("%w: %v", ErrNotARemote, err)
		}
		if u.Scheme != "https" && u.Scheme != "ssh" {
			return Remote{}, fmt.Errorf("%w: the scheme %q", ErrNotARemote, u.Scheme)
		}
		if u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.Contains(raw, "#") {
			return Remote{}, fmt.Errorf("%w: it has a query or fragment", ErrNotARemote)
		}
		host, path = u.Hostname(), u.Path
		if u.Scheme == "https" {
			port = u.Port()
		}
	} else {
		before, after, ok := strings.Cut(raw, ":")
		if !ok {
			return Remote{}, fmt.Errorf("%w: neither a URL, scp-like nor an absolute path", ErrNotARemote)
		}
		if at := strings.LastIndex(before, "@"); at >= 0 {
			if !userName.MatchString(before[:at]) {
				return Remote{}, fmt.Errorf("%w: the user name", ErrNotARemote)
			}
			before = before[at+1:]
		}
		host, path = before, after
	}
	host = strings.ToLower(host)
	if len(host) > maxHost || !hostName.MatchString(host) {
		return Remote{}, fmt.Errorf("%w: the host", ErrNotARemote)
	}
	if port != "" {
		for _, r := range port {
			if r < '0' || r > '9' {
				return Remote{}, fmt.Errorf("%w: the port", ErrNotARemote)
			}
		}
	}
	repo := strings.Trim(path, "/")
	repo = strings.TrimSuffix(repo, ".git")
	if !repoPath.MatchString(repo) {
		return Remote{}, fmt.Errorf("%w: the repository path", ErrNotARemote)
	}
	for _, seg := range strings.Split(repo, "/") {
		if seg == "." || seg == ".." {
			return Remote{}, fmt.Errorf("%w: the repository path", ErrNotARemote)
		}
	}
	web := "https://" + host
	if port != "" {
		web += ":" + port
	}
	r := Remote{Host: host, Repo: repo, Web: web + "/" + repo, Kind: Gitea}
	switch {
	case host == "github.com":
		r.Kind = GitHub
	case strings.Split(host, ".")[0] == "gitlab":
		r.Kind = GitLab
	}
	return r, nil
}

// Command is the line an export job runs to open its pull request.
type Command struct {
	// CLI is the forge's command line tool.
	CLI string
	// Line is a POSIX shell line run in the job's folder: every value one
	// single-quoted --flag=value, the title and body from title.txt and
	// body.md.
	Line string
}

// CLIFor names the command line tool of a kind of forge; "" for none.
func CLIFor(kind string) string {
	switch kind {
	case GitHub:
		return "gh"
	case GitLab:
		return "glab"
	case Gitea:
		return "tea"
	}
	return ""
}

// maxValue is the longest value For puts on a command line.
const maxValue = 1000

// literal reports whether v can be put into single quotes as it is: not
// empty, not too long, without a quote, line break or hidden character.
func literal(v string) bool {
	return v != "" && len(v) <= maxValue && utf8.ValidString(v) &&
		!strings.ContainsFunc(v, func(r rune) bool { return r == '\'' || r == ' ' || textsafe.IsHidden(r) })
}

func quote(flag, v string) string { return flag + "='" + v + "'" }

// For returns the command an export job runs to open a pull request from
// branch into base on r, and true; false when r is a local path, the
// forge's tool is not found by lookPath (which is never run) or a value
// could not be passed literally.
func For(r Remote, branch, base string, lookPath func(string) (string, error)) (Command, bool) {
	cli := CLIFor(r.Kind)
	if cli == "" || !repoPath.MatchString(r.Repo) || !literal(branch) || !literal(base) {
		return Command{}, false
	}
	if _, err := lookPath(cli); err != nil {
		return Command{}, false
	}
	const title = `--title="$(cat title.txt)"`
	var args []string
	switch r.Kind {
	case GitHub:
		args = []string{"gh pr create", quote("--repo", r.Repo), quote("--head", branch), quote("--base", base),
			title, quote("--body-file", "body.md")}
	case GitLab:
		repo := r.Repo
		if r.Host != "gitlab.com" {
			// glab finds a host other than gitlab.com only from a full
			// address.
			if !literal(r.Web) {
				return Command{}, false
			}
			repo = r.Web
		}
		args = []string{"glab mr create", quote("--repo", repo), quote("--source-branch", branch), quote("--target-branch", base),
			title, `--description="$(cat body.md)"`, "--yes"}
	case Gitea:
		args = []string{"tea pulls create", quote("--repo", r.Repo), quote("--head", branch), quote("--base", base),
			title, `--description="$(cat body.md)"`}
	}
	return Command{CLI: cli, Line: strings.Join(args, " ")}, true
}

// ErrLocalRemote is CheckAddress's answer for a remote that is a folder on
// this machine: no pull request address fits it.
var ErrLocalRemote = errors.New("the remote is a folder on this machine, not a forge: there is no pull request address to report")

// CheckAddress checks the address an export job reports for its pull
// request on r, the remote omo pushed to: it must be an https address on
// r's host, without user or password, whose path lies inside the
// repository (it starts with /<r.Repo>/, compared case-insensitively, and
// has no "." or ".." segment or backslash), so that a job cannot leave a
// link to another host, another repository or another page of the forge on
// the ticket. A local remote (no host) accepts no address at all
// (ErrLocalRemote).
func CheckAddress(r Remote, address string) error {
	if r.Host == "" {
		return ErrLocalRemote
	}
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil {
		return fmt.Errorf("the address of the pull request must be an https address on %s", r.Host)
	}
	if strings.ToLower(u.Hostname()) != r.Host {
		return fmt.Errorf("the address of the pull request must be on %s, the host of the project's remote", r.Host)
	}
	inside := fmt.Errorf("the address of the pull request must be a page of the repository %s on %s", r.Repo, r.Host)
	prefix := "/" + strings.ToLower(r.Repo) + "/"
	if strings.ContainsRune(address, '\\') || !strings.HasPrefix(strings.ToLower(u.Path), prefix) || len(u.Path) == len(prefix) {
		return inside
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return inside
		}
	}
	return nil
}
