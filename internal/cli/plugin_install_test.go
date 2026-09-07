package cli

import "testing"

func TestPluginInstallExposesBranchFlag(t *testing.T) {
	cmd, _, err := Root("test").Find([]string{"plugin", "install"})
	if err != nil {
		t.Fatal(err)
	}
	flag := cmd.Flags().Lookup("branch")
	if flag == nil {
		t.Fatal("plugin install has no --branch flag")
	}
	if flag.Usage == "" {
		t.Fatal("--branch flag has no help text")
	}
}
