package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDbgSpawn(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "sit tight")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	t.Logf("state=%s", agentState(t, o, name))
	raw, _ := os.ReadFile(filepath.Join(o.Dir, ".omo", "logs", name+".log"))
	t.Logf("LOG:\n%s", raw)
}
