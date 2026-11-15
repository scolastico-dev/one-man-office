package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The export push sends the commit omo pinned at hand-in to the project's
// remote. Agents can write the project repository's config (git config in a
// worktree writes the shared file) and its hooks folder. What omo takes from
// that config is the remote's name and address only, read by RemoteFor
// without includes: the address is where the user's repository says the
// project lives, and an agent able to change it can already push anywhere
// with the user's credentials from its terminal. Nothing else of the
// project's config is read by the push: PushCommit runs git in an empty
// repository omo makes in a private folder, which borrows the project's
// objects (GIT_ALTERNATE_OBJECT_DIRECTORIES) and has no config, hooks or
// remotes of its own, and pushes to the address itself, not to a remote
// name. So hooks (pre-push and the rest, core.hooksPath), core.sshCommand,
// credential helpers, url.*.insteadOf and pushInsteadOf, remote.*.pushurl
// and remote.*.push, push.default, aliases and include/includeIf of the
// project's config do not apply to it. The user's global and system config
// (credential helpers, ssh settings) do.

// ErrPushRejected marks a push the remote refused (the branch there is not
// an ancestor of the commit, or a rule of the remote).
var ErrPushRejected = errors.New("the remote refused the push")

// ErrBadRemote marks a remote name or address omo does not push to.
var ErrBadRemote = errors.New("not a remote omo pushes to")

// Remote is where a project pushes.
type Remote struct{ Name, URL string }

// Address is the remote's URL without user or password, for messages.
func (r Remote) Address() string { return HideCredentials(oneLine(r.URL)) }

// remoteName is the shape of a remote name omo pushes to.
var remoteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// RemoteFor finds the push remote of base in the repository at repo:
// branch.<base>.pushRemote, remote.pushDefault, branch.<base>.remote,
// origin. The name must match ^[A-Za-z0-9][A-Za-z0-9._-]*$; URL is the
// first remote.<name>.pushurl, else the first remote.<name>.url, and must
// pass ValidSourceURL (a local path is allowed). Only the repository's own
// config file is read, includes not followed; repo must not lie in a folder
// agents control (ErrAgentFolder).
func (g *Git) RemoteFor(ctx context.Context, repo, base string) (Remote, error) {
	if base == "" || ValidRef(base) != nil {
		return Remote{}, fmt.Errorf("%w: the base branch %q is not a branch name", ErrBadRemote, oneLine(base))
	}
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return Remote{}, err
	}
	entries, err := g.configEntries(ctx, filepath.Join(common, "config"))
	if err != nil {
		return Remote{}, err
	}
	last := map[string]string{}
	first := map[string]string{}
	for _, e := range entries {
		key := configKey(e[0])
		last[key] = e[1]
		if _, ok := first[key]; !ok {
			first[key] = e[1]
		}
	}
	name := ""
	for _, key := range []string{"branch." + base + ".pushremote", "remote.pushdefault", "branch." + base + ".remote"} {
		if v, ok := last[key]; ok && v != "" {
			name = v
			break
		}
	}
	if name == "" {
		name = "origin"
	}
	if !remoteName.MatchString(name) {
		return Remote{}, fmt.Errorf("%w: the remote name %q", ErrBadRemote, oneLine(name))
	}
	u, ok := first["remote."+name+".pushurl"]
	if !ok || u == "" {
		u = first["remote."+name+".url"]
	}
	if u == "" {
		return Remote{}, fmt.Errorf("%w: the repository names no address for the remote %s", ErrBadRemote, name)
	}
	if err := ValidSourceURL(u); err != nil {
		return Remote{}, fmt.Errorf("%w: the remote %s: %v", ErrBadRemote, name, err)
	}
	return Remote{Name: name, URL: u}, nil
}

// configKey is the name of a config entry as `git config --list` prints
// it, with section and key in lower case and the subsection as it is.
func configKey(name string) string {
	first := strings.Index(name, ".")
	last := strings.LastIndex(name, ".")
	if first < 0 || first == last {
		return strings.ToLower(name)
	}
	return strings.ToLower(name[:first]) + name[first:last] + strings.ToLower(name[last:])
}

// pushConfig is put before git push: no hooks, no tags, no submodules, no
// signing, no push options.
var pushConfig = []string{
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "core.fsmonitor=false",
	"-c", "core.askPass=",
	"-c", "push.followTags=false",
	"-c", "push.recurseSubmodules=no",
	"-c", "push.gpgSign=false",
	"-c", "push.pushOption=",
	"-c", "submodule.recurse=false",
}

// PushCommit pushes commit to refs/heads/<branch> of r from the repository
// at repo, never with force and without hooks:
// git push --porcelain --no-verify --end-of-options <address>
// <commit>:refs/heads/<branch>, run in an empty repository of omo's that
// borrows repo's objects, so nothing of repo's config applies (see the
// note above). commit must be a full object id; branch must pass ValidRef
// and start with "omo/"; r must be what RemoteFor returns. A rejected push
// is ErrPushRejected; a push that ran out of time wraps ErrTimeout. Every
// URL in an error has lost its user and password.
func (g *Git) PushCommit(ctx context.Context, repo string, r Remote, commit, branch string) error {
	if !commitID.MatchString(commit) {
		return fmt.Errorf("%q is not a full commit id", oneLine(commit))
	}
	if !strings.HasPrefix(branch, "omo/") || len(branch) == len("omo/") || ValidRef(branch) != nil {
		return fmt.Errorf("%w: %q is not a branch omo pushes", ErrBadRef, oneLine(branch))
	}
	if !remoteName.MatchString(r.Name) {
		return fmt.Errorf("%w: the remote name %q", ErrBadRemote, oneLine(r.Name))
	}
	if err := ValidSourceURL(r.URL); err != nil {
		return fmt.Errorf("%w: the remote %s: %v", ErrBadRemote, r.Name, err)
	}
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return err
	}
	objects := filepath.Join(common, "objects")
	if fi, err := os.Stat(objects); err != nil || !fi.IsDir() {
		return fmt.Errorf("the repository %s has no objects folder", repo)
	}
	tmp, err := os.MkdirTemp("", "omo-push-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	env := append([]string{
		"GIT_DIR=" + tmp,
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(tmp),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + objects,
	}, noPromptEnv...)
	o := cmdOpts{ctx: ctx, timeout: g.op, dir: tmp, env: env, cLocale: true}
	if _, _, err := g.exec(o, append(append([]string(nil), pushConfig...), "init", "--quiet", "--bare", "--template=")...); err != nil {
		return fmt.Errorf("preparing the push: %s", gitMessage(err))
	}
	refspec := commit + ":refs/heads/" + branch
	args := append(append([]string(nil), pushConfig...), "push", "--porcelain", "--no-verify", "--no-follow-tags",
		"--recurse-submodules=no", "--no-signed")
	if isAbsLocal(r.URL) {
		// A repository on this machine runs its own hooks in the
		// receiving git; that one gets no hooks either.
		args = append(args, "--receive-pack=git -c core.hooksPath="+os.DevNull+" receive-pack")
	}
	args = append(args, "--end-of-options", r.URL, refspec)
	stdout, stderr, err := g.exec(o, args...)
	if err == nil {
		return nil
	}
	where := fmt.Sprintf("pushing %s to %s (%s) as %s", commit[:12], r.Name, r.Address(), branch)
	if cerr := ctx.Err(); cerr != nil {
		return &cleanError{msg: where + ": " + cerr.Error(), err: cerr}
	}
	if errors.Is(err, ErrTimeout) {
		return &cleanError{msg: where + ": timed out", err: ErrTimeout}
	}
	if line, ok := porcelainLine(stdout, "refs/heads/"+branch); ok && strings.HasPrefix(line, "!") {
		why := ""
		if f := strings.Split(line, "\t"); len(f) >= 3 {
			why = " " + strings.TrimSpace(f[2])
		}
		return &cleanError{msg: fmt.Sprintf("%s: %s%s", where, ErrPushRejected, HideCredentials(oneLine(why))), err: ErrPushRejected}
	}
	return &cleanError{msg: fmt.Sprintf("%s failed: %s", where, gitText(stderr+stdout))}
}

// porcelainLine finds the line of git push --porcelain about the remote ref
// dst: "<flag>\t<src>:<dst>\t<summary>".
func porcelainLine(out, dst string) (string, bool) {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) >= 2 && strings.HasSuffix(f[1], ":"+dst) {
			return l, true
		}
	}
	return "", false
}

// credentialsInURL is the user information of a URL: everything between
// "<scheme>://" and the last "@" before the host's end.
var credentialsInURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/?#\s'"<>]*@`)

// HideCredentials removes user and password from every URL in s.
func HideCredentials(s string) string {
	return credentialsInURL.ReplaceAllString(s, "$1")
}
