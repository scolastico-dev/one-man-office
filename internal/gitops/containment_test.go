package gitops

import (
	"path/filepath"
	"testing"
)

// A merge policy that discards source content is not evidence of content
// inclusion. Both proof paths must ignore policies from every attribute
// source, including overrides of Git's named built-in drivers.
func TestContainmentIgnoresMergePolicies(t *testing.T) {
	testContainmentMergePolicies(t)
}

func testContainmentMergePolicies(t *testing.T) {
	t.Helper()
	for _, policy := range []string{"tracked-custom", "tracked-text-override", "tracked-nested", "info-custom", "worktree-custom", "global-custom", "global-default", "tracked-union"} {
		t.Run(policy, func(t *testing.T) {
			g := testGit(t)
			repo := newTestRepo(t)
			name := "f.txt"
			files := map[string]string{name: "base\n"}
			driver, exported := "keep", "exported feature\n"
			switch policy {
			case "tracked-custom":
				files[".gitattributes"] = "f.txt merge=keep\n"
			case "tracked-text-override":
				files[".gitattributes"] = "f.txt merge=text\n"
				driver = "text"
			case "tracked-nested":
				name = "nested/f.txt"
				files[name] = "base\n"
				files["nested/.gitattributes"] = "f.txt merge=keep\n"
			case "info-custom":
				write(t, filepath.Join(repo, ".git", "info", "attributes"), "f.txt merge=keep\n")
			case "worktree-custom":
				write(t, filepath.Join(repo, ".gitattributes"), "f.txt merge=keep\n")
			case "global-custom":
				attrs := filepath.Join(t.TempDir(), "attributes")
				write(t, attrs, "f.txt merge=keep\n")
				g = gitWithGlobal(t, "[core]\n\tattributesFile = "+filepath.ToSlash(attrs)+"\n[merge \"keep\"]\n\tdriver = true\n")
			case "global-default":
				g = gitWithGlobal(t, "[merge]\n\tdefault = keep\n[merge \"keep\"]\n\tdriver = true\n")
			case "tracked-union":
				files[".gitattributes"] = "f.txt merge=union\n"
				exported = ""
			}
			start := commitOn(t, repo, "main", "proof base", files)
			commit := commitOn(t, repo, "exported", "pending feature", map[string]string{name: exported})
			target := commitOn(t, repo, "main", "different target edit", map[string]string{name: "unrelated target content\n"})
			tg(t, repo, "config", "merge."+driver+".driver", "true")
			before, beforeRefs, beforeObjects := snapshot(t, repo), refs(t, repo), objectCount(t, repo)
			config := read(t, filepath.Join(repo, ".git", "config"))
			if got, err := g.ExportContained(repo, start, commit, start); err != nil || got {
				t.Fatalf("baseline ExportContained = %v, %v; want false", got, err)
			}
			if got, err := g.ExportContained(repo, start, commit, target); err != nil || got {
				t.Errorf("unmerged ExportContained = %v, %v; want false", got, err)
			}
			if got, err := g.SquashApplied(repo, target, commit); err != nil || got {
				t.Errorf("unmerged SquashApplied = %v, %v; want false", got, err)
			}
			sameSnapshot(t, "containment proof", before, snapshot(t, repo))
			if refs(t, repo) != beforeRefs || objectCount(t, repo) != beforeObjects || read(t, filepath.Join(repo, ".git", "config")) != config {
				t.Error("containment proof changed repository objects, refs or config")
			}
			if policy == "info-custom" && read(t, filepath.Join(repo, ".git", "info", "attributes")) != "f.txt merge=keep\n" {
				t.Error("containment proof changed project info/attributes")
			}
		})
	}
}
