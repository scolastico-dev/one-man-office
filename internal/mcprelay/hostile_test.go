package mcprelay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

func TestAHugeToolListIsCut(t *testing.T) {
	f := newFixture(t, func(t *tunables) { t.maxTools = 20 }, stdioServer("many", map[string]string{fakemcp.EnvMode: string(fakemcp.ModeMany), fakemcp.EnvCount: "3000"}))
	tools, err := f.pool.Tools(context.Background(), agent("s", t.TempDir(), api.RoleDeveloper))
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 20 {
		t.Fatalf("%d tools offered, want 20", len(tools))
	}
	st := f.state("many")
	if len(st.Omitted) == 0 || !strings.Contains(st.Omitted[0].Reason, "more than 20 tools") {
		t.Fatalf("omitted %+v", st.Omitted[:min(3, len(st.Omitted))])
	}
}

func TestAHugeResultIsRefused(t *testing.T) {
	f := newFixture(t, func(t *tunables) {
		t.maxResult = 1 << 20
		t.maxMessage = 3 << 20
	}, stdioServer("fake", nil))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	res := f.call(id, "fake__big", map[string]int{"bytes": 2 << 20})
	if !res.IsError || !strings.Contains(resultText(res), "more than omo relays (1.0 MiB)") {
		t.Fatalf("a 2 MiB result answered %.200q", resultText(res))
	}
	// Larger than omo reads of one message: the connection is dropped,
	// the process restarted.
	res = f.call(id, "fake__big", map[string]int{"bytes": 5 << 20})
	if !res.IsError || len(resultText(res)) > 1000 {
		t.Fatalf("a 5 MiB message answered %.200q", resultText(res))
	}
	eventually(t, "the server to be usable again", func() bool {
		res := f.call(id, "fake__echo", map[string]string{"text": "ok"})
		return !res.IsError
	})
}

func TestAServerThatSpeaksGarbageIsContained(t *testing.T) {
	s := stdioServer("garbage", map[string]string{fakemcp.EnvMode: string(fakemcp.ModeGarbage)})
	s.Timeouts.Startup = api.Duration(500 * time.Millisecond)
	f := newFixture(t, nil, s, stdioServer("fine", nil))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	tools, err := f.pool.Tools(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	names := toolNames(tools)
	if !slices.Contains(names, "fine__echo") || slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, "garbage__") }) {
		t.Fatalf("tools %v", names)
	}
	if st := f.state("garbage"); st.LastError == "" {
		t.Fatalf("state %+v", st)
	}
	res := f.call(id, "garbage__echo", nil)
	if !res.IsError {
		t.Fatal("a garbage server answered")
	}
}

func TestASlowStartIsLeftOutAndAnnouncedLater(t *testing.T) {
	s := stdioServer("slow", map[string]string{fakemcp.EnvDelay: "700ms"})
	s.Timeouts.Startup = api.Duration(5 * time.Second)
	f := newFixture(t, nil, s, stdioServer("fine", nil))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	tools, _ := f.pool.Tools(ctx, id)
	if slices.ContainsFunc(toolNames(tools), func(n string) bool { return strings.HasPrefix(n, "slow__") }) {
		t.Fatal("a server that was not ready is listed")
	}
	eventually(t, "the slow server to be announced", func() bool {
		return slices.Contains(f.state("slow").Tools, "slow__echo")
	})
	tools, _ = f.pool.Tools(context.Background(), id)
	if !slices.Contains(toolNames(tools), "slow__echo") {
		t.Fatalf("tools %v", toolNames(tools))
	}
}

func TestToolNamesNeverTakeAnOmoToolsName(t *testing.T) {
	f := newFixture(t, nil, stdioServer("collide", map[string]string{fakemcp.EnvMode: string(fakemcp.ModeCollide)}))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	tools, err := f.pool.Tools(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	names := toolNames(tools)
	omo := map[string]bool{}
	for _, c := range aicmd.Commands() {
		omo[c.Tool] = true
	}
	for _, n := range names {
		if omo[n] || !strings.HasPrefix(n, "collide"+Separator) {
			t.Errorf("relayed tool %s", n)
		}
		if len(clientToolPrefix)+len(n) > maxClientToolName {
			t.Errorf("relayed tool %s is too long", n)
		}
	}
	for _, want := range []string{"collide__ticket_show", "collide__done", "collide__a__b", "collide__we_rd", "collide__we_rd_2", "collide__we_rd_3"} {
		if !slices.Contains(names, want) {
			t.Errorf("tools %v lack %s", names, want)
		}
	}
	st := f.state("collide")
	reasons := map[string]string{}
	for _, o := range st.Omitted {
		reasons[o.Tool] = o.Reason
	}
	if !strings.Contains(reasons["bad_schema"], "not a JSON object schema") || !strings.Contains(reasons[strings.Repeat("l", 130)], "longer than") {
		t.Fatalf("omitted %+v", st.Omitted)
	}
	// Each name reaches its own upstream tool.
	for name, upstream := range map[string]string{"collide__we_rd": "we!rd", "collide__we_rd_2": "we?rd", "collide__we_rd_3": "we_rd", "collide__ticket_show": "ticket_show"} {
		if got := resultText(f.call(id, name, nil)); got != "called "+upstream {
			t.Errorf("%s reached %q", name, got)
		}
	}
}

func TestOfferToolsFiltersAndRenames(t *testing.T) {
	schema := map[string]any{"type": "object"}
	upstream := []*mcp.Tool{
		{Name: "keep", InputSchema: schema, Description: "Keeps.", OutputSchema: map[string]any{"type": "string"}},
		{Name: "deny", InputSchema: schema},
		{Name: "other", InputSchema: schema},
		{Name: "keep", InputSchema: schema},
		{Name: "no schema"},
		nil,
	}
	set := offerTools("srv", api.MCPToolFilter{Allow: []string{"keep", "deny", "no schema"}, Deny: []string{"deny"}}, upstream, 10)
	if got := set.names(); !slices.Equal(got, []string{"srv__keep"}) {
		t.Fatalf("names %v", got)
	}
	tool := set.tools[0].Tool
	if tool.Description != "Keeps." || tool.OutputSchema != nil {
		t.Fatalf("tool %+v", tool)
	}
	if set.upstream["srv__keep"] != "keep" {
		t.Fatalf("upstream %v", set.upstream)
	}
	if len(set.omitted) != 2 {
		t.Fatalf("omitted %+v", set.omitted)
	}
	long := offerTools("srv", api.MCPToolFilter{}, []*mcp.Tool{{Name: "d", InputSchema: schema, Description: strings.Repeat("é", 5000)}}, 10)
	if d := long.tools[0].Tool.Description; len(d) > maxDescription+10 || !strings.HasSuffix(d, "…") {
		t.Fatalf("a long description is %d bytes", len(d))
	}
}

func TestSanitizeToolName(t *testing.T) {
	for in, want := range map[string]string{"a.b-c_D9": "a.b-c_D9", "a b/ä": "a_b__", "": "_", "x‮y": "x_y"} {
		if got := sanitizeToolName(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAHugeHTTPAnswerIsCut(t *testing.T) {
	big := strings.Repeat("x", 3<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"x":"` + big + `"}}`))
	}))
	t.Cleanup(srv.Close)
	s := httpServer("huge", srv.URL)
	s.Timeouts.Startup = api.Duration(2 * time.Second)
	f := newFixture(t, func(t *tunables) { t.maxMessage = 1 << 20 }, s)
	res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "huge__x", nil)
	if !res.IsError || len(resultText(res)) > 1000 {
		t.Fatalf("answered %.300q", resultText(res))
	}
}

func TestLogLinesAreBoundedAndClean(t *testing.T) {
	f := newFixture(t, nil, stdioServer("fake", nil))
	id := agent("s", t.TempDir(), api.RoleDeveloper)
	f.call(id, "fake__stderr", map[string]string{"text": "\x1b[31mred\x1b[0m " + strings.Repeat("y", 10000)})
	eventually(t, "the stderr line", func() bool { return strings.Contains(f.logText("fake"), "stderr: ?[31mred") })
	for _, line := range strings.Split(f.logText("fake"), "\n") {
		if len(line) > logMaxLine+100 || strings.ContainsRune(line, 0x1b) {
			t.Fatalf("log line of %d bytes: %.100q", len(line), line)
		}
	}
	lines, err := f.pool.Log("fake", 1000)
	if err != nil || len(lines) == 0 || lines[0].At.IsZero() {
		t.Fatalf("log %v %v", lines, err)
	}
	if _, err := f.pool.Log("../etc", 10); err == nil {
		t.Fatal("a name outside the log folder was read")
	}
}

func TestLogRotates(t *testing.T) {
	dir := t.TempDir()
	l := newServerLog(dir, "rot", &redactor{})
	line := strings.Repeat("z", 4000)
	for range logMaxBytes/4000 + 10 {
		l.line(line)
	}
	l.close()
	lines, err := readLog(l.path, 5)
	if err != nil || len(lines) != 5 {
		t.Fatalf("lines %d, %v", len(lines), err)
	}
	all, _ := readLog(l.path, 1<<20)
	if len(all) != logMaxBytes/4000+10 {
		t.Fatalf("%d lines across segments", len(all))
	}
}
