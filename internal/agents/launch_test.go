package agents

import (
	"reflect"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Every supported program gets its start adapter (prompt delivery, folder
// trust); a profile of a program missing from prepareLaunch would start
// like a custom command and stop at the program's trust question.
func TestEverySupportedCLIGetsItsStartAdapter(t *testing.T) {
	for _, provider := range agentcli.Supported {
		spec := Spec{CLI: string(provider), Command: "wrapper", Args: []string{"--x"}, Dir: "/work/repo",
			StartPrompt: "run omo ai ready", Delivery: Delivery{Inject: true}, MCPBridge: "/bin/omo"}
		got := prepareLaunch(spec)
		want := agentcli.Prepare(provider, "wrapper", []string{"--x"}, map[string]string{}, "/work/repo", "run omo ai ready", true, true, "/bin/omo")
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: launch = %+v, want the adapter's %+v", provider, got, want)
		}
	}
	custom := prepareLaunch(Spec{CLI: api.CLICustom, Command: "agy", Args: []string{"--x"}, Dir: "/work/repo", StartPrompt: "p", Delivery: Delivery{Inject: true}, MCPBridge: "/bin/omo"})
	if custom.PromptInjected || !reflect.DeepEqual(custom.Args, []string{"--x"}) {
		t.Errorf("custom launch = %+v", custom)
	}
}
