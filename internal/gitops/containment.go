package gitops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// containmentTree computes content inclusion independently of merge
// policies. A successful configured merge can intentionally discard the
// source, so its tree is not evidence that the source's content landed.
// The private bare repository borrows only objects. Its highest-priority
// attributes select the built-in text merger with boolean `merge` (a
// named `merge=text` could itself select a custom driver), and disable
// filters/conversion. No project config, attributes or index are touched.
// A conflict is inconclusive and returns an empty tree without an error.
// base is optional; when present it is the recorded export merge base.
func (g *Git) containmentTree(repo, base, target, source string) (string, error) {
	common, err := g.commonDir(context.Background(), repo)
	if err != nil {
		return "", err
	}
	format, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "omo-containment-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	o := cmdOpts{timeout: g.op, dir: tmp, env: []string{
		"GIT_DIR=" + tmp,
		"GIT_COMMON_DIR=" + tmp,
		"GIT_INDEX_FILE=" + filepath.Join(tmp, "index"),
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(tmp),
		"GIT_OBJECT_DIRECTORY=" + filepath.Join(tmp, "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(common, "objects"),
		"GIT_ATTR_NOSYSTEM=1",
	}}
	config := []string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "core.bare=true",
		"-c", "core.attributesFile=" + os.DevNull,
		"-c", "merge.renormalize=false",
	}
	run := func(args ...string) (string, error) {
		out, _, err := g.exec(o, append(append([]string(nil), config...), args...)...)
		return out, err
	}
	if _, err := run("init", "--quiet", "--bare", "--template=", "--object-format="+strings.TrimSpace(format)); err != nil {
		return "", err
	}
	o.env = append(o.env, "GIT_WORK_TREE="+tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "info"), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, "info", "attributes"), []byte("* merge -filter -text -eol -ident -working-tree-encoding\n"), 0600); err != nil {
		return "", err
	}
	args := []string{"merge-tree", "--write-tree", "--no-messages"}
	if base != "" {
		args = append(args, "--merge-base="+base)
	}
	out, err := run(append(args, target, source)...)
	if base != "" && exitCode(err) == 129 {
		// Git 2.38/2.39 lack --merge-base. Temporary commits with the
		// same sole parent force the recorded base without changing refs.
		tips := make([]string, 2)
		o.stdin = "omo: temporary content containment check\n"
		o.env = append(o.env, "GIT_AUTHOR_NAME=omo", "GIT_AUTHOR_EMAIL=omo@localhost", "GIT_COMMITTER_NAME=omo", "GIT_COMMITTER_EMAIL=omo@localhost")
		for i, id := range []string{target, source} {
			tip, err := run("commit-tree", id+"^{tree}", "-p", base, "--no-gpg-sign", "-F", "-")
			if err != nil {
				return "", err
			}
			tips[i] = strings.TrimSpace(tip)
		}
		o.stdin = ""
		out, err = run("merge-tree", "--write-tree", "--no-messages", tips[0], tips[1])
	}
	if exitCode(err) == 1 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(out)
	if !fullID(tree) {
		return "", fmt.Errorf("git merge-tree returned no tree")
	}
	return tree, nil
}
