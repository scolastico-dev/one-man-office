package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/branchname"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// BranchName derives the branch of a ticket from the branch template
// (the general setting branches.template, see package branchname) and the
// ticket's values v. The name it gives is checked against git's rules for
// a branch name; a template that cannot be used, or a name that is not a
// valid branch, is an error naming why.
//
// exists reports whether a name is taken. A taken name gets -2, -3, ...
// appended until one is free, at most up to -maxBranchSuffix; past that, or
// when exists fails, BranchName returns an error instead of a name. A nil
// exists takes nothing. Uniqueness comes only from exists, which the caller
// answers under the repository's lock (see gitops.Git.AddWorktreeNewBranch).
func BranchName(tmpl string, v branchname.Vars, exists func(string) (bool, error)) (string, error) {
	base, err := branchname.Expand(tmpl, v)
	if err != nil {
		return "", err
	}
	if exists == nil {
		return base, nil
	}
	for i := 1; i <= maxBranchSuffix; i++ {
		name := base
		if i > 1 {
			name += "-" + strconv.Itoa(i)
		}
		if err := branchname.Valid(name); err != nil {
			return "", err
		}
		taken, err := exists(name)
		if err != nil {
			var conflict *gitops.PathConflictError
			if errors.As(err, &conflict) {
				return "", err
			}
			return "", fmt.Errorf("cannot check whether branch %s exists: %w", name, err)
		}
		if !taken {
			return name, nil
		}
	}
	return "", fmt.Errorf("the branches %s to %s-%d all exist", base, base, maxBranchSuffix)
}

// branchVars are the values of the branch template for ticket t in project.
func branchVars(t board.EngineTicket, project string) branchname.Vars {
	host, user := branchname.Machine()
	return branchname.Vars{Number: t.ID, Title: t.Title, Type: string(t.Type), Project: project, Host: host, User: user}
}

// maxBranchSuffix is the highest number BranchName appends to a taken name.
const maxBranchSuffix = 100

// WorktreeDir is the folder of ticket n's worktree of project below root,
// the worktrees folder of omo's home.
func WorktreeDir(root, project string, n int64) string {
	return filepath.Join(root, project, strconv.FormatInt(n, 10))
}

// WorkdirDir is the working folder of ticket n, a task without a project,
// below root, the working folders' folder of omo's home.
func WorkdirDir(root string, n int64) string {
	return filepath.Join(root, strconv.FormatInt(n, 10))
}
