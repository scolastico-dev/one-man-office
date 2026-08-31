package cli

import (
	"bytes"
	"testing"
)

func TestWriteOfficeExitReason(t *testing.T) {
	var out bytes.Buffer
	if !writeOfficeExitReason(&out, "usage hard limit reached") {
		t.Fatal("non-empty exit reason was not written")
	}
	if got := out.String(); got != "omo exited: usage hard limit reached\n" {
		t.Fatalf("exit output = %q", got)
	}
	out.Reset()
	if writeOfficeExitReason(&out, "  ") || out.Len() != 0 {
		t.Fatalf("blank reason produced output %q", out.String())
	}
}
