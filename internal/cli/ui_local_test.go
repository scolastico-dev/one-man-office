package cli

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/service"
)

func TestUILocalStartReturnsOnlyMachineReadableAttachedInstance(t *testing.T) {
	isolate(t)
	r := startRoot(t)
	out := mustExecute(t, "ui-local", "start")
	var instance service.Instance
	decoder := json.NewDecoder(strings.NewReader(out))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&instance); err != nil {
		t.Fatalf("native start did not return JSON: %v, %q", err, out)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("extra native output: %v", err)
	}
	if instance.Owned || instance.Endpoint == "" || instance.Token == "" || !strings.Contains(r.out.String(), instance.URL) {
		t.Fatalf("attached instance = %+v", instance)
	}
	// The hidden machine interface must leave standalone lifecycle usable.
	if _, running, err := service.Running(context.Background()); err != nil || !running {
		t.Fatalf("attach changed standalone service: %v", err)
	}
}

func TestUILocalStartDoesNotPromptOrModifyLegacyHome(t *testing.T) {
	machine := buildOldMachine(t, "")
	home := machine.home
	before := snapshotTree(t, home)
	out, err := execute(context.Background(), "ui-local", "start")
	if err == nil || out != "" {
		t.Fatalf("legacy start = %q, %v", out, err)
	}
	if after := snapshotTree(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("native start modified legacy home")
	}
}
