// Package fakeagent is a tiny scenario-driven stand-in for a real AI CLI.
// Integration tests and --mock offices run it instead of claude.
package fakeagent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

// Embedded per-role default scenarios for --mock offices: a minimal but
// complete org-chart pass (CEO → PM → developer → reviewer → merge).
var autoScenarios = map[string]string{
	"ceo": `ready
jobcreate|product_manager|demo spec|Build the demo feature: create hello.txt containing "hello".|
sleep|1h`,
	"product_manager": `ready
jobcreate|developer|create hello.txt|Create hello.txt containing "hello", commit it.|$REPO|$JOB
wait`,
	"developer": `ready
shell|echo hello > hello.txt && git add hello.txt && git commit -m "feat: hello"
done|hello.txt created and committed
wait`,
	"reviewer": `ready
verdict|merge|looks good
done|merged`,
	"freelancer": `ready
done|nothing to do
wait`,
	"smokealarm": `ready
done|round complete: 0 incidents`,
	"firefighter": `ready
resolvefirst|auto-resolved by mock firefighter
done|incident resolved`,
}

type state struct {
	socket  string
	agentID string
	jobID   int64
	prompt  string
	out     io.Writer
}

// Run executes a scenario file, or the embedded default for autoRole.
func Run(out io.Writer, scenarioPath, autoRole string) error {
	socket, agentID, err := sockc.Env()
	if err != nil {
		return err
	}
	var script string
	switch {
	case scenarioPath != "":
		raw, err := os.ReadFile(scenarioPath)
		if err != nil {
			return err
		}
		script = string(raw)
	case autoRole != "":
		s, ok := autoScenarios[autoRole]
		if !ok {
			return fmt.Errorf("no auto scenario for role %q", autoRole)
		}
		script = s
	default:
		return fmt.Errorf("need --scenario or --auto-role")
	}
	st := &state{socket: socket, agentID: agentID, out: out}
	sc := bufio.NewScanner(strings.NewReader(script))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := st.step(line); err != nil {
			return fmt.Errorf("step %q: %w", line, err)
		}
	}
	return nil
}

func (st *state) call(verb string, args, resp any) error {
	return sockc.Call(st.socket, st.agentID, verb, args, resp)
}

func (st *state) step(line string) error {
	f := strings.Split(line, "|")
	op := f[0]
	arg := func(i int) string {
		if i < len(f) {
			return f[i]
		}
		return ""
	}
	switch op {
	case "ready":
		var r proto.ReadyResponse
		if err := st.call("ready", nil, &r); err != nil {
			return err
		}
		st.jobID, st.prompt = r.JobID, r.Prompt
		fmt.Fprintln(st.out, r.Prompt)
		return nil
	case "sleep":
		d, err := time.ParseDuration(arg(1))
		if err != nil {
			return err
		}
		time.Sleep(d)
		return nil
	case "send":
		return st.call("send", proto.SendArgs{To: arg(1), Subject: arg(2), Body: arg(3), Priority: "normal"}, nil)
	case "jobcreate":
		repo := arg(4)
		if repo == "$REPO" {
			// --mock works in any office: the supervisor passes a real repo
			// key from the config rather than assuming one is called "demo".
			repo = os.Getenv("OMO_MOCK_REPO")
		}
		parent := int64(0)
		if p := arg(5); p == "$JOB" {
			parent = st.jobID
		} else if p != "" {
			parent, _ = strconv.ParseInt(p, 10, 64)
		}
		return st.call("job.create", proto.JobCreateArgs{
			Role: arg(1), Title: arg(2), Goal: arg(3), Repo: repo, Parent: parent}, nil)
	case "verdict":
		return st.call("job.verdict", proto.VerdictArgs{JobID: st.jobID, Verdict: arg(1), Notes: arg(2)}, nil)
	case "incident":
		return st.call("incident.create", proto.IncidentCreateArgs{Agent: arg(1), Class: arg(2), Detail: arg(3)}, nil)
	case "resolvefirst":
		m := regexp.MustCompile(`INCIDENT_ID: (\d+)`).FindStringSubmatch(st.prompt)
		if m == nil {
			return fmt.Errorf("no INCIDENT_ID in ready prompt")
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		return st.call("incident.resolve", proto.IncidentResolveArgs{ID: id, Report: arg(1)}, nil)
	case "agentkill":
		return st.call("agent.kill", proto.AgentNameArgs{Name: arg(1)}, nil)
	case "pause":
		return st.call("office.pause", nil, nil)
	case "resume":
		return st.call("office.resume", nil, nil)
	case "shell":
		cmd := exec.Command("sh", "-c", strings.Join(f[1:], "|"))
		cmd.Stdout, cmd.Stderr = st.out, st.out
		return cmd.Run()
	case "done":
		return st.call("done", proto.DoneArgs{Result: arg(1)}, nil)
	case "wait":
		var w proto.WaitResponse
		return st.call("wait", nil, &w)
	case "hang":
		// Deliberately never returns. A plain `select {}` would trip Go's
		// deadlock detector and exit the process instead of hanging.
		for {
			time.Sleep(time.Hour)
		}
	default:
		return fmt.Errorf("unknown scenario op %q", op)
	}
}
