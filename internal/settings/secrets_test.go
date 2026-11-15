package settings

import (
	"reflect"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestIsSecret(t *testing.T) {
	cases := []struct {
		name, value string
		want        bool
	}{
		{"ANTHROPIC_API_KEY", "sk-ant-1", true},
		{"anthropic_api_key", "sk-ant-1", true},
		{"GITHUB_TOKEN", "ghp_1", true},
		{"Client_Secret", "x", true},
		{"DB_PASSWORD", "x", true},
		{"DB_PASSWD", "x", true},
		{"GOOGLE_APPLICATION_CREDENTIALS", "/x.json", true},
		{"AUTH", "x", true},
		{"BASIC_AUTH", "x", true},
		{"OAUTH_CLIENT", "x", true},
		{"HTTP_AUTHORIZATION", "Bearer x", true},
		{"AUTHOR_AUTH", "x", true},
		{"ANTHROPIC_API_KEY", "`pass show anthropic`", false},
		{"ANTHROPIC_API_KEY", "$OTHER_KEY", false},
		{"ANTHROPIC_API_KEY", "${OTHER_KEY:-x}", false},
		{"ANTHROPIC_API_KEY", "", false},
		{"PATH", "/usr/bin", false},
		{"GIT_AUTHOR_NAME", "Me", false},
		{"GIT_AUTHOR_EMAIL", "me@example.invalid", false},
	}
	for _, c := range cases {
		if got := IsSecret(c.name, c.value); got != c.want {
			t.Errorf("IsSecret(%q, %q) = %v, want %v", c.name, c.value, got, c.want)
		}
	}
}

func TestHideEnv(t *testing.T) {
	env := map[string]string{"API_KEY": "sk", "TOKEN_CMD_KEY": "`pass x`", "PATH": "/bin"}
	got := HideEnv(env)
	want := map[string]string{"API_KEY": api.HiddenValue, "TOKEN_CMD_KEY": "`pass x`", "PATH": "/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HideEnv = %v", got)
	}
	if env["API_KEY"] != "sk" {
		t.Fatal("HideEnv changed its argument")
	}
	if HideEnv(nil) == nil {
		t.Fatal("HideEnv(nil) is nil")
	}
}

func TestRestoreEnv(t *testing.T) {
	stored := map[string]string{"API_KEY": "sk", "PATH": "/bin", "REF_KEY": "$X"}
	got, err := RestoreEnv(map[string]string{"API_KEY": api.HiddenValue, "PATH": "/usr/bin", "NEW": "v"}, stored, "agent_env")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"API_KEY": "sk", "PATH": "/usr/bin", "NEW": "v"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RestoreEnv = %v", got)
	}
	// A hidden value with no stored secret of that name: a new or renamed
	// variable, or one whose stored value is no secret.
	for _, name := range []string{"OTHER_KEY", "PATH", "REF_KEY"} {
		_, err := RestoreEnv(map[string]string{name: api.HiddenValue}, stored, "models.a.env")
		fe, ok := err.(*FieldError)
		if !ok || fe.Field != "models.a.env."+name || fe.Message != "Enter the value of "+name+" again." {
			t.Errorf("%s: err = %#v", name, err)
		}
	}
	if got, err := RestoreEnv(nil, stored, "env"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("RestoreEnv(nil) = %v, %v", got, err)
	}
}
