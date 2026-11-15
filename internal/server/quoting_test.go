package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// TestDecodeQuotesNoValue sends bodies whose parser errors quote what they
// could not read through decode, which every other route's body passes: the
// refusal names the field at most.
func TestDecodeQuotesNoValue(t *testing.T) {
	type target struct {
		D api.Duration `json:"d"`
		N int          `json:"n"`
		F float32      `json:"f"`
		S string       `json:"s"`
		O api.Optional[int]
	}
	for _, body := range []string{
		`{"d":"` + canaryGeneral + `"}`,
		`{"n":` + canaryNumber + `}`,
		`{"f":1` + canaryNumber + `e99}`,
		`{"s":"x","O":` + canaryNumber + `}`,
		`{"s":"` + canaryProfile + `"` + canaryModel + `}`,
		`"` + canaryGeneral + `"`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var v target
		err := decode(httptest.NewRecorder(), r, &v)
		if err == nil {
			t.Fatalf("%s: accepted", body)
		}
		var e *api.Error
		if !asAPIError(err, &e) || e.Code != api.CodeBadRequest {
			t.Fatalf("%s: %v, want bad_request", body, err)
		}
		noCanary(t, body, e.Message)
	}
}

func asAPIError(err error, e **api.Error) bool { return errors.As(err, e) }

// brokenFiles are settings files with a canary where a value cannot be
// read: each must be reported by its file and line or field only. Prompt
// files are not among them: their text is answered as it is, and a prompt
// that does not render is reported by the engine (see the engine's
// TestCustomPromptErrorIsBounded).
var brokenFiles = map[string]string{
	"settings.yaml":         "max_running_agents: 2\nhealth:\n  silent_after: " + canaryGeneral + "\nworkspace_roots:\n  - path: /a\n    ignore: [\"**" + canaryModel + "\"]\n",
	"profiles/broken.yaml":  "cli: custom\ncommand: x\nprompt_delivery:\n  delay: " + canaryProfile + "\nmodels:\n  a: {}\n",
	"profiles/number.yaml":  "cli: custom\ncommand: x\nmodels:\n  a:\n    args: " + canaryNumber + "\n",
	"roles/developer.yaml":  "models: [\"" + canaryModel + "\"]\n",
	"projects/broken.yaml":  "name: [" + canaryGeneral + "]\npath: /p\n",
	"settings.yaml.unknown": canaryGeneral,
}

// TestBrokenSettingsFilesQuoteNothing opens a server on a home whose
// settings files hold canaries where they cannot be read, then breaks them
// again by hand while it runs: no log line, event or answer of any route
// holds a canary.
func TestBrokenSettingsFilesQuoteNothing(t *testing.T) {
	var logs syncBuffer
	l := testHome(t)
	write := func(suffix string) {
		for name, content := range brokenFiles {
			path := filepath.Join(l.Root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content+suffix), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("")
	s, err := newServer(Options{Layout: l, Config: testConfig(), Version: "test"}, &logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if len(s.settings.Errors()) == 0 {
		t.Fatal("no broken file reported")
	}
	// A hand edit while the server runs.
	write("# edited\n")
	s.settings.Reload()
	s.waitSettingsEvents(context.Background())

	b := newBrowser(t, s)
	for _, name := range []string{"claude", "developer", "broken"} {
		for _, row := range s.routeTable {
			method, path, _ := strings.Cut(row.pattern, " ")
			if method != http.MethodGet {
				continue
			}
			path = wildcard.ReplaceAllString(path, name)
			noCanary(t, row.pattern, readBody(t, asUser(b, s, method, path, nil)))
		}
		noCanary(t, "preview", readBody(t, asUser(b, s, "POST", "/api/prompts/"+name+"/preview", map[string]any{})))
	}
	evs, err := db.EventsSince(s.db, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	invalid := 0
	for _, e := range evs {
		if e.Kind == api.KindSettingsInvalid {
			invalid++
		}
		noCanary(t, "event "+e.Kind, e.Detail)
	}
	if invalid == 0 {
		t.Fatal("no settings.invalid event")
	}
	noCanary(t, "the server log", logs.String())
}
