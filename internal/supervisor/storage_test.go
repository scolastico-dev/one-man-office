package supervisor

import (
	"path/filepath"
	"testing"
)

func TestPersistentRolesUseOfficeStorageWorkdir(t *testing.T) {
	o := newOffice(t, nil)
	want := filepath.Join(o.Dir, ".omo", "storage")
	for _, role := range []string{"ceo", "product_manager", "smokealarm", "firefighter"} {
		got, err := o.Sup.roleWorkDir(role, o.Dir)
		if err != nil {
			t.Fatalf("%s workdir: %v", role, err)
		}
		if got != want {
			t.Errorf("%s workdir = %q, want %q", role, got, want)
		}
	}
	requested := filepath.Join(o.Dir, "developer-worktree")
	if got, err := o.Sup.roleWorkDir("developer", requested); err != nil || got != requested {
		t.Fatalf("developer workdir = %q, err %v", got, err)
	}
}
