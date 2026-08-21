package db

import "testing"

func TestShutdownContextUpsertAndDelete(t *testing.T) {
	d := open(t)
	value := ShutdownContext{Role: "developer", JobID: 7, Agent: "developer-ada", Context: "tests remain"}
	if err := SaveShutdownContext(d, value); err != nil {
		t.Fatal(err)
	}
	value.Context = "only commit remains"
	if err := SaveShutdownContext(d, value); err != nil {
		t.Fatal(err)
	}
	got, err := GetShutdownContext(d, "developer", 7)
	if err != nil || got == nil || got.Agent != "developer-ada" || got.Context != "only commit remains" {
		t.Fatalf("context = %+v, err %v", got, err)
	}
	if err := DeleteShutdownContext(d, got.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := GetShutdownContext(d, "developer", 7); err != nil || got != nil {
		t.Fatalf("deleted context = %+v, err %v", got, err)
	}
}

func TestShutdownContextsDoNotCollideForSameRoleWithoutJobs(t *testing.T) {
	d := open(t)
	for _, agent := range []string{"smoke-one", "smoke-two"} {
		if err := SaveShutdownContext(d, ShutdownContext{Role: "smokealarm", Agent: agent, Context: agent + " handoff"}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := GetShutdownContext(d, "smokealarm", 0)
	if err != nil || first == nil {
		t.Fatalf("first context = %+v, err %v", first, err)
	}
	if err := DeleteShutdownContext(d, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := GetShutdownContext(d, "smokealarm", 0)
	if err != nil || second == nil || second.Agent == first.Agent {
		t.Fatalf("second context = %+v, err %v", second, err)
	}
}
