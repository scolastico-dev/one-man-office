package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestDeveloperLogsAreAvailableToUserCEOAndFirefighter(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":         "ready\nsleep|60s\n",
		"developer":   "ready\nshell|echo developer-log-marker\nsleep|60s\n",
		"firefighter": "ready\nsleep|60s\n",
	})
	ceo, _ := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run")
	developer, _ := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	ff, _ := o.Sup.Spawn("firefighter", "firefighter", 0, o.Dir, "INCIDENT_ID: 1\ninspect")
	waitFor(t, 5*time.Second, "agents ready", func() bool {
		return agentState(t, o, ceo) == "working" && agentState(t, o, developer) == "working" && agentState(t, o, ff) == "working"
	})

	args := proto.LogTailArgs{Name: developer, Lines: 20}
	for _, caller := range []string{"user", ceo, ff} {
		var response proto.LogTailResponse
		waitFor(t, 5*time.Second, "developer log visible to "+caller, func() bool {
			response = proto.LogTailResponse{}
			return sockc.Call(o.Sup.SocketPath, caller, "agent.logs", args, &response) == nil &&
				strings.Contains(strings.Join(response.Lines, "\n"), "developer-log-marker")
		})
	}
}

func TestDeveloperLogsRejectOtherRolesAndInactiveTargets(t *testing.T) {
	o := newOffice(t, map[string]string{
		"developer":  "ready\nsleep|60s\n",
		"freelancer": "ready\nsleep|60s\n",
	})
	developer, _ := o.Sup.Spawn("developer", "developer", 0, o.Dir, "work")
	freelancer, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	waitFor(t, 5*time.Second, "agents ready", func() bool {
		return agentState(t, o, developer) == "working" && agentState(t, o, freelancer) == "working"
	})
	if err := sockc.Call(o.Sup.SocketPath, freelancer, "agent.logs", proto.LogTailArgs{Name: developer, Lines: 20}, nil); err == nil {
		t.Fatal("freelancer read developer logs")
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "agent.logs", proto.LogTailArgs{Name: freelancer, Lines: 20}, nil); err == nil {
		t.Fatal("user read a non-developer log")
	}
}
