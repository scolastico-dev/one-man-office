package server

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

// branchWorkers bounds how many projects are read from git at once when
// projects are listed.
const branchWorkers = 8

// remoteHeadTimeout bounds the read of a remote's recorded HEAD. It is the
// git client's limit for read-only queries.
const remoteHeadTimeout = 10 * time.Second

// defaultBranchRemote is the remote whose recorded HEAD names the default
// branch.
const defaultBranchRemote = "origin"

// withBranches returns a copy of ps with CurrentBranch and DefaultBranch read
// from git, up to branchWorkers projects at once. It runs git, so it must be
// called without any lock held; a missing project is left without branches.
func (s *Server) withBranches(ps []api.Project) []api.Project {
	out := slices.Clone(ps)
	sem := make(chan struct{}, branchWorkers)
	var wg sync.WaitGroup
	for i := range out {
		if out[i].Missing {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i].CurrentBranch, out[i].DefaultBranch = s.branches(out[i].Path)
		}()
	}
	wg.Wait()
	return out
}

// branches returns the branch checked out in the repository at path and the
// repository's default branch; each is "" when unknown. The current branch
// is "" on a detached HEAD. The default branch is the one the remote
// "origin" recorded as its HEAD when the repository was cloned or last
// asked (no network access), else an existing main, else an existing
// master, else "".
func (s *Server) branches(path string) (current, def string) {
	if b, err := s.git.CurrentBranch(path); err == nil {
		current = b
	}
	if def = remoteHeadBranch(path, defaultBranchRemote); def != "" {
		return current, def
	}
	for _, name := range []string{"main", "master"} {
		if ok, err := s.git.BranchExists(path, name); err == nil && ok {
			return current, name
		}
	}
	return current, ""
}

// remoteHeadBranch returns the branch that refs/remotes/<remote>/HEAD points
// to in the repository at path, or "" when there is none. It reads only the
// local repository.
func remoteHeadBranch(path, remote string) string {
	ctx, cancel := context.WithTimeout(context.Background(), remoteHeadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
	cmd.Dir = path
	cmd.Env = session.ProcessEnvironment([]string{"GIT_TERMINAL_PROMPT=0"})
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	name, ok := strings.CutPrefix(strings.TrimSpace(string(out)), remote+"/")
	if !ok || name == "" || name == "HEAD" {
		return ""
	}
	return name
}
