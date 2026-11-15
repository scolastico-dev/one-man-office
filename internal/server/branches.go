package server

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
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
// from git. At most branchWorkers projects are read at once across the whole
// server, however many callers there are. It runs git, so it must be called
// without any lock held. A missing project is left without branches, and so
// is every project not started before ctx ended; each project's read ends
// by itself after remoteHeadTimeout, so a hung repository holds back its
// caller by about one git call.
func (s *Server) withBranches(ctx context.Context, ps []api.Project) []api.Project {
	out := slices.Clone(ps)
	var wg sync.WaitGroup
	defer wg.Wait()
	for i := range out {
		if out[i].Missing {
			continue
		}
		select {
		case s.branchSem <- struct{}{}:
		case <-ctx.Done():
			return out
		}
		if ctx.Err() != nil {
			<-s.branchSem
			return out
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.branchSem }()
			rctx, cancel := context.WithTimeout(ctx, remoteHeadTimeout)
			defer cancel()
			out[i].CurrentBranch, out[i].DefaultBranch = s.readBranches(rctx, out[i].Path)
		}()
	}
	return out
}

// branches returns the branch checked out in the repository at path and the
// repository's default branch; each is "" when unknown. The current branch
// is "" on a detached HEAD. The default branch is the one the remote
// "origin" recorded as its HEAD when the repository was cloned or last
// asked (no network access), else an existing main, else an existing
// master, else "". No further git call starts once ctx has ended.
func (s *Server) branches(ctx context.Context, path string) (current, def string) {
	if ctx.Err() != nil {
		return "", ""
	}
	if b, err := s.git.CurrentBranch(path); err == nil {
		current = b
	}
	if ctx.Err() != nil {
		return current, ""
	}
	if def = s.remoteHeadBranch(ctx, path, defaultBranchRemote); def != "" {
		return current, def
	}
	for _, name := range []string{"main", "master"} {
		if ctx.Err() != nil {
			return current, ""
		}
		if ok, err := s.git.BranchExists(path, name); err == nil && ok {
			return current, name
		}
	}
	return current, ""
}

// remoteHeadBranch returns the branch that refs/remotes/<remote>/HEAD points
// to in the repository at path, or "" when there is none. It reads only the
// local repository.
func (s *Server) remoteHeadBranch(ctx context.Context, path, remote string) string {
	ctx, cancel := context.WithTimeout(ctx, remoteHeadTimeout)
	defer cancel()
	return s.git.RemoteHeadBranch(ctx, path, remote)
}
