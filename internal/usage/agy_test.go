package usage

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func agyJob() Job {
	return Job{Script: "agy", Text: Defaults()["agy"],
		Profile: api.Profile{Name: "agy", CLI: api.CLIAgy, Command: "agy", UsageScript: "agy", Env: map[string]string{"AGY_PLAIN": "x"}}}
}

// agyAnswers is an AgyUsage that answers with out and records its calls.
func agyAnswers(out []byte, err error, calls *[]string) func(context.Context, string, []string) ([]byte, error) {
	return func(_ context.Context, command string, env []string) ([]byte, error) {
		*calls = append(*calls, command)
		if !slices.Contains(env, "AGY_PLAIN=x") {
			return nil, errors.New("the profile environment is missing")
		}
		return out, err
	}
}

func agyRun(t *testing.T, out []byte, err error) (Result, []string, error) {
	t.Helper()
	cleanEnv(t)
	var calls []string
	res, runErr := Run(context.Background(), agyJob(), RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t), AgyUsage: agyAnswers(out, err, &calls)})
	return res, calls, runErr
}

// changedAgy returns the recorded answer with change applied.
func changedAgy(t *testing.T, change func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(fixture(t, "agy-usage.json"), &m); err != nil {
		t.Fatal(err)
	}
	change(m)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The recorded answer of `agy -p /usage --output-format json` gives one
// window per quota group, named by the bucket id and labelled by the group.
func TestAgyDefaultScriptReadsQuotaGroups(t *testing.T) {
	res, calls, err := agyRun(t, fixture(t, "agy-usage.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"agy"}) {
		t.Fatalf("calls = %v", calls)
	}
	want := []Window{
		{Name: "gemini-weekly", Label: "Gemini models, weekly", ResetsAt: time.Date(2026, 11, 23, 19, 44, 19, 0, time.UTC)},
		{Name: "3p-weekly", Label: "Claude and GPT models, weekly", UsedPercent: 0, ResetsAt: time.Date(2026, 11, 23, 19, 47, 34, 0, time.UTC)},
	}
	if len(res.Windows) != len(want) {
		t.Fatalf("windows = %+v", res.Windows)
	}
	for i, w := range want {
		got := res.Windows[i]
		if got.Name != w.Name || got.Label != w.Label || !got.ResetsAt.Equal(w.ResetsAt) {
			t.Errorf("window %d = %+v, want %+v", i, got, w)
		}
	}
	if used := res.Windows[0].UsedPercent; used < 1.43 || used > 1.44 {
		t.Errorf("gemini-weekly used = %v, want about 1.435", used)
	}
	if res.Windows[1].UsedPercent != 0 {
		t.Errorf("3p-weekly used = %v", res.Windows[1].UsedPercent)
	}
	log := strings.Join(res.Log, "\n")
	if !strings.Contains(log, "Claude and GPT models: Models within this group: Claude Sonnet 4.6, Claude Opus 4.6, GPT-OSS 120B") {
		t.Errorf("log = %q", log)
	}
}

func TestAgyDefaultScriptOddAnswers(t *testing.T) {
	cases := []struct {
		name    string
		out     []byte
		runErr  error
		wantErr string
		limited bool
	}{
		{"command failed", nil, errors.New("exit status 1"), "the agy usage command failed", false},
		{"not JSON", []byte("Please log in"), nil, "agy did not answer JSON", false},
		{"rate limited text", []byte("Error 429: RESOURCE_EXHAUSTED"), nil, RateLimitText, true},
		{"failed status", changedAgy(t, func(m map[string]any) { m["status"] = "ERROR"; m["response"] = "boom" }), nil, "agy answered status ERROR", false},
		{"rate limited status", changedAgy(t, func(m map[string]any) { m["status"] = "ERROR"; m["response"] = "HTTP 429 RESOURCE_EXHAUSTED" }), nil, RateLimitText, true},
		{"a model turn instead of /usage", changedAgy(t, func(m map[string]any) { delete(m, "command") }), nil, "agy did not run its /usage command; this agy may be too old or too new", false},
		{"no groups", changedAgy(t, func(m map[string]any) { m["command"].(map[string]any)["data"] = map[string]any{} }), nil, "agy reported no quota groups", false},
		{"no usable bucket", changedAgy(t, func(m map[string]any) {
			for _, g := range m["command"].(map[string]any)["data"].(map[string]any)["groups"].([]any) {
				for _, b := range g.(map[string]any)["buckets"].([]any) {
					delete(b.(map[string]any), "remaining_fraction")
				}
			}
		}), nil, "agy reported no usable limit", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := agyRun(t, c.out, c.runErr)
			var limited *RateLimitError
			if err == nil || !strings.Contains(err.Error(), c.wantErr) || errors.As(err, &limited) != c.limited {
				t.Fatalf("err = %v, want %q (rate limited %v)", err, c.wantErr, c.limited)
			}
		})
	}
}

// A bucket id that is no window name becomes one; a reset time that cannot
// be read is left out with a log line; another period gives its own label.
func TestAgyDefaultScriptCleansNames(t *testing.T) {
	out := changedAgy(t, func(m map[string]any) {
		groups := m["command"].(map[string]any)["data"].(map[string]any)["groups"].([]any)
		b := groups[0].(map[string]any)["buckets"].([]any)[0].(map[string]any)
		b["id"] = "Gemini Daily!"
		b["window"] = "daily"
		b["reset_time"] = "tomorrow"
		groups[1].(map[string]any)["buckets"].([]any)[0].(map[string]any)["id"] = "_x"
	})
	res, _, err := agyRun(t, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Windows) != 2 || res.Windows[0].Name != "gemini_daily_" || res.Windows[0].Label != "Gemini models, daily" || !res.Windows[0].ResetsAt.IsZero() ||
		res.Windows[1].Name != "group_2" {
		t.Fatalf("windows = %+v", res.Windows)
	}
	if !strings.Contains(strings.Join(res.Log, "\n"), "the reset time of gemini_daily_ could not be read") {
		t.Fatalf("log = %q", res.Log)
	}
}

// omo.agy_usage is for agy profiles only, runs once per run and reports a
// timeout and too much output in its own words.
func TestAgyUsageHelper(t *testing.T) {
	cleanEnv(t)
	script := `function usage()
		local a, e1 = omo.agy_usage()
		local b, e2 = omo.agy_usage()
		error(tostring(a) .. "|" .. tostring(e1) .. "|" .. tostring(b) .. "|" .. tostring(e2), 0)
	end`
	var calls []string
	o := RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t), AgyUsage: agyAnswers([]byte("{}"), nil, &calls)}
	j := agyJob()
	j.Text = script
	_, err := Run(context.Background(), j, o)
	if err == nil || err.Error() != "{}|nil|nil|agy_usage runs at most once per run" {
		t.Fatalf("err = %v", err)
	}
	for _, cli := range []string{api.CLIClaude, api.CLICodex, api.CLIGemini, api.CLICustom} {
		j.Profile.CLI = cli
		_, err := Run(context.Background(), j, o)
		if err == nil || err.Error() != "nil|agy_usage is only available for agy profiles|nil|agy_usage is only available for agy profiles" {
			t.Fatalf("%s: err = %v", cli, err)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %v", calls)
	}
	j.Profile.CLI = api.CLIAgy
	j.Text = `function usage() local a, e = omo.agy_usage() error(tostring(e), 0) end`
	o.AgyUsage = func(ctx context.Context, _ string, _ []string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	o.Timeout = 200 * time.Millisecond
	if _, err := Run(context.Background(), j, o); err == nil || !strings.Contains(err.Error(), "time") {
		t.Fatalf("timeout: err = %v", err)
	}
	o.Timeout = 0
	o.AgyUsage = func(context.Context, string, []string) ([]byte, error) { return nil, errAgyTooLarge }
	if _, err := Run(context.Background(), j, o); err == nil || err.Error() != "the agy usage command printed too much" {
		t.Fatalf("too large: err = %v", err)
	}
}
