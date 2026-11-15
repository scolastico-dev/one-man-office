package gitops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Commit is one commit of a log: its full object id and the first line of
// its message, as git prints it (author text: callers that show it clean
// it first).
type Commit struct {
	ID      string
	Subject string
}

// FirstParentLog lists the commits on the first-parent chain of tip that
// stop does not contain, newest first, at most limit of them (limit <= 0
// lists none), and returns how many there are in all. It only reads.
func (g *Git) FirstParentLog(repo, tip, stop string, limit int) ([]Commit, int, error) {
	tipID, err := g.commitOf(repo, tip)
	if err != nil {
		return nil, 0, err
	}
	stopID, err := g.commitOf(repo, stop)
	if err != nil {
		return nil, 0, err
	}
	out, _, err := g.exec(cmdOpts{timeout: g.op, dir: repo},
		"rev-list", "--first-parent", "--count", tipID, "^"+stopID)
	if err != nil {
		return nil, 0, err
	}
	total, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return nil, 0, fmt.Errorf("git rev-list --count printed %q", strings.TrimSpace(out))
	}
	if total == 0 || limit <= 0 {
		return nil, total, nil
	}
	out, _, err = g.exec(cmdOpts{timeout: g.op, dir: repo},
		"-c", "i18n.logOutputEncoding=UTF-8", "-c", "log.showSignature=false",
		"log", "-z", "--first-parent", "--no-decorate", "--no-color", "--format=%H %s",
		"--max-count="+strconv.Itoa(limit), tipID, "^"+stopID, "--")
	if err != nil {
		return nil, 0, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		id, subject, _ := strings.Cut(rec, " ")
		commits = append(commits, Commit{ID: id, Subject: subject})
	}
	return commits, total, nil
}

// RemoteHeadBranch returns the branch that refs/remotes/<remote>/HEAD points
// to in the repository at repo, or "" when there is none or ctx ends first.
// It reads only the local repository.
func (g *Git) RemoteHeadBranch(ctx context.Context, repo, remote string) string {
	out, _, err := g.exec(cmdOpts{ctx: ctx, timeout: g.query, dir: repo, env: []string{"GIT_TERMINAL_PROMPT=0"}},
		"symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return ""
	}
	name, ok := strings.CutPrefix(strings.TrimSpace(out), remote+"/")
	if !ok || name == "" || name == "HEAD" {
		return ""
	}
	return name
}
