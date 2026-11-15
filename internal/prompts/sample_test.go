package prompts

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// roleFields are the fields the engine sets for a role prompt of an agent
// that works on a ticket (and for the restart note rendered with the same
// data); ceoFields those it sets for the CEO.
var (
	roleFields = []string{
		"Name", "Role", "CEOEnabled", "CEOMayApprove", "TaskRelease", "Instructions",
		"Ticket", "Type", "Title", "ReviewRound", "Supervisor", "AgentReview", "Verification",
		"Story", "StoryTitle", "MultiProject", "Projects", "RestartNote",
	}
	ceoFields = []string{"Name", "Role", "CEOEnabled", "CEOMayApprove", "TaskRelease", "Instructions"}
)

// engineFields lists, per prompt, the fields of Data the engine fills when
// it renders that prompt (engine: ready.go, start.go, wait.go, health.go,
// stop.go). SampleData must not set any other field: a preview would then
// show text an agent never gets.
var engineFields = map[string][]string{
	"ceo": ceoFields, "pm": roleFields, "developer": roleFields, "reviewer": roleFields, "export": roleFields,
	"ceo-instructions": ceoFields, "pm-instructions": roleFields, "developer-instructions": roleFields,
	"reviewer-instructions": roleFields, "export-instructions": roleFields,
	"restart":           roleFields,
	"start":             {"Name", "Role", "Ticket"},
	"handoff":           {"Name", "Role", "Ticket"},
	"wait-timeout":      {"Name", "Role", "Ticket", "WaitTimeout", "Timeouts"},
	"wait-investigate":  {"Name", "Role", "Ticket", "WaitTimeout", "Timeouts"},
	"stalled":           {"Name", "Role", "Ticket", "Silent"},
	"question-reminder": {"Question", "Ticket"},
}

func TestSampleDataUsesOnlyEngineFields(t *testing.T) {
	for _, name := range Names {
		allowed, ok := engineFields[name]
		if !ok {
			t.Fatalf("no engine fields listed for %s", name)
		}
		v := reflect.ValueOf(SampleData(name))
		for i := range v.NumField() {
			field := v.Type().Field(i).Name
			if !v.Field(i).IsZero() && !slices.Contains(allowed, field) {
				t.Errorf("SampleData(%s) sets %s, which the engine never sets for it", name, field)
			}
		}
	}
}

func TestEveryDefaultRendersWithSampleData(t *testing.T) {
	defaults := Defaults()
	for _, name := range Names {
		if Kind(name) == "instructions" {
			continue
		}
		out, err := Render(name, defaults[name], SampleData(name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Contains(out, "<no value>") || strings.TrimSpace(out) == "" && name != "restart" {
			t.Errorf("%s renders badly with sample data:\n%s", name, out)
		}
		if err := Check(name, defaults[name]); err != nil {
			t.Errorf("Check refuses the default of %s: %v", name, err)
		}
	}
	// The sample worktrees live where omo puts them.
	d := SampleData("developer")
	if len(d.Projects) != 2 || !strings.Contains(d.Projects[0].Worktree, "/.local/omo/worktrees/") {
		t.Errorf("developer sample projects = %+v", d.Projects)
	}
}

func TestCheckRefusesBrokenTemplate(t *testing.T) {
	for _, text := range []string{"{{ .Nope }}", "{{ if }}", "{{ end }}", "Hello {{ .Name"} {
		if err := Check("developer", text); err == nil {
			t.Errorf("Check(%q) = nil", text)
		}
	}
	if err := Check("developer-instructions", "Use {{ freely, it is plain text"); err != nil {
		t.Errorf("instructions refused: %v", err)
	}
	if err := Check("start", "You are {{.Name}}."); err != nil {
		t.Errorf("a good message prompt refused: %v", err)
	}
	if err := Check("nope", "text"); err == nil {
		t.Error("an unknown prompt name passed the check")
	}
}

func TestCheckBoundsTemplates(t *testing.T) {
	cases := map[string]string{
		"too large":         strings.Repeat("x", maxTemplateSize+1),
		"range over number": "{{range 1000000000}}{{end}}",
		"range over int":    "{{range .Ticket}}x{{end}}",
		"range over var":    "{{$p := .Projects}}{{range $p}}x{{end}}",
		"deep range":        "{{range .Projects}}{{range $.Projects}}{{range $.Projects}}x{{end}}{{end}}{{end}}",
		"define":            `{{define "a"}}{{template "a"}}{{end}}{{template "a"}}`,
		"template call":     `{{template "developer"}}`,
		"block":             `{{block "a" .}}x{{end}}`,
		"wide printf":       `{{printf "%0999999999d" 1}}`,
		"star printf":       `{{printf "%*d" 1000000000 1}}`,
		"printf from data":  `{{printf .Title}}`,
		"nested printf":     `{{if (printf "%099999d" 1)}}x{{end}}`,
	}
	for label, text := range cases {
		t.Run(label, func(t *testing.T) {
			start := time.Now()
			err := Check("developer", text)
			if err == nil {
				t.Fatalf("Check(%.60q) = nil", text)
			}
			if time.Since(start) > time.Second {
				t.Errorf("the check took %s", time.Since(start))
			}
		})
	}
	for _, ok := range []string{
		"{{range .Projects}}{{.Name}}{{end}}",
		"{{range $i, $p := .Projects}}{{$i}} {{$p.Name}}{{range $.Projects}}{{.ID}}{{end}}{{end}}",
		`{{printf "%-10s|%5d" .Name .Ticket}}`,
		`{{with index .Projects 0}}{{.Parent}}{{end}}`,
	} {
		if err := Check("developer", ok); err != nil {
			t.Errorf("Check(%q) = %v", ok, err)
		}
	}
}

func TestCheckBoundsOutput(t *testing.T) {
	// About 200 KiB of template, four times over two projects: past the
	// output bound.
	text := "{{range .Projects}}{{range $.Projects}}" + strings.Repeat("{{$.Title}}{{$.StoryTitle}}{{$.Name}}", 6000) + "{{end}}{{end}}"
	if len(text) > maxTemplateSize {
		t.Fatalf("test template too large: %d", len(text))
	}
	err := Check("developer", text)
	if err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("Check = %v, want the output bound", err)
	}
}

func TestTemplateErrorsQuoteLittle(t *testing.T) {
	long := "{{ .Nope" + strings.Repeat(" | printf \"%s\"", 400) + " }}"
	err := Check("developer", long)
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	if utf8.RuneCountInString(msg) > maxMessage+1 || strings.Contains(msg, "\n") {
		t.Errorf("message too long or on several lines (%d): %q", len(msg), msg)
	}
	_, err = Preview("developer", "line one\n{{ .Nope }}", "")
	if err == nil || !strings.Contains(err.Error(), "Nope") {
		t.Errorf("preview error = %v, want it to name the field", err)
	}
}

func TestPromptPreviewMatchesRender(t *testing.T) {
	defaults := Defaults()
	for _, name := range Names {
		if Kind(name) == "instructions" {
			continue
		}
		got, err := Preview(name, defaults[name], "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, err := Render(name, defaults[name], SampleData(name))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("Preview(%s) differs from Render with sample data", name)
		}
	}
	// Instructions appear inside the role prompt in effect, verbatim (they
	// are never parsed as a template).
	instructions := "Write commit messages in German. {{.Nope}}"
	got, err := Preview("developer-instructions", "  "+instructions+"\n", defaults["developer"])
	if err != nil {
		t.Fatal(err)
	}
	d := SampleData("developer")
	d.Instructions = instructions
	want, _ := Render("developer", defaults["developer"], d)
	if got != want || !strings.HasSuffix(got, "\n"+instructions) {
		t.Errorf("instructions preview:\n%s", got)
	}
	// A broken role prompt in effect is named as such.
	if _, err := Preview("developer-instructions", "x", "{{ .Nope }}"); err == nil || !strings.Contains(err.Error(), "developer prompt") {
		t.Errorf("broken role prompt: %v", err)
	}
	if _, err := Preview("nope", "x", ""); err == nil {
		t.Error("unknown prompt previewed")
	}
}

// TestTemplateErrorsQuoteNoText feeds templates whose errors Go words with a
// piece of the template (a string literal, the command it stopped at) and
// requires a message that names the line, never the text.
func TestTemplateErrorsQuoteNoText(t *testing.T) {
	const secret = "sk-SECRET-1"
	for _, text := range []string{
		"one\n{{ printf \"" + secret + "\" .Nope }}",
		"one\n{{ .Title \"" + secret + "\" }}",
		"{{ \"" + secret + "\" | nope }}",
		"{{ if \"" + secret + "\" }}",
		"{{ .Title }} {{ \"" + secret + "\" \"" + secret + "\" }}",
		"{{ $x := \"" + secret + "\" }}{{ $x.Nope }}",
		"{{ 1" + secret + " }}",
	} {
		for what, err := range map[string]error{"check": Check("developer", text), "preview": func() error { _, err := Preview("developer", text, ""); return err }()} {
			if err == nil {
				t.Fatalf("%s %q: no error", what, text)
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Errorf("%s %q: message quotes the template: %v", what, text, err)
			}
		}
	}
	err := Check("developer", "one\n{{ printf \""+secret+"\" .Nope }}")
	if err == nil || !strings.HasPrefix(err.Error(), "line 2: ") {
		t.Errorf("message %v, want it to name line 2", err)
	}
}
