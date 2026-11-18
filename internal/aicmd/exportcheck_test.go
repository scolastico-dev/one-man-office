package aicmd

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

func TestCheckExportCommand(t *testing.T) {
	c, ok := byTool("ticket_check_export")
	if !ok {
		t.Fatal("missing manual export check command")
	}
	if !c.CommandLineOnly || c.Sees != nil {
		t.Fatal("user-only command exposed to agents")
	}
	client := apiclient.NewWithTransport(apiclient.HandlerTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/tickets/7/check-export" {
			t.Errorf("wrong request %s %s", r.Method, r.URL)
		}
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, `{"checking":true}`)
	})), "")
	result, err := c.Run(context.Background(), client, proxyTestInput(c, `{"ticket":7}`, t))
	if err != nil || !strings.Contains(result.Text, "Merge check requested") {
		t.Fatalf("result %+v %v", result, err)
	}
}
