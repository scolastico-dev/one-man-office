package board

import (
	"context"
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestParseMentions(t *testing.T) {
	cases := map[string][]string{
		"@pm please check":                  {"pm"},
		"ping @Developer and @PM, also @pm": {"developer", "pm"},
		"mail me at a@pm.com":               nil,
		"see `@reviewer` in code":           nil,
		"```\n@ceo\n```\n@user":             {"user"},
		"@types/node and @pm.":              {"pm"},
		"@pmx @supervisor!":                 {"supervisor"},
		// More edges.
		"":                                 nil,
		"@":                                nil,
		"@@pm":                             nil,
		"x@pm":                             nil,
		"ü@pm":                             nil,
		"_@pm -@pm /@pm .@pm":              nil,
		"(@pm) [@user]":                    {"pm", "user"},
		"@pm2 @pm_x @pm-x @pm/x @pm.x":     nil,
		"@pm.\n@ceo:":                      {"pm", "ceo"},
		"@PM @Pm @pM":                      {"pm"},
		"@user @reviewer @user":            {"user", "reviewer"},
		"~~~\n@ceo\n~~~\n@pm":              {"pm"},
		"````\n```\n@ceo\n````\n@pm":       {"pm"},
		"```go\n@ceo\n":                    nil, // unclosed fence runs to the end
		"   ```\n@ceo\n   ```\n@user":      {"user"},
		"a ``code with ` @pm`` @user":      {"user"},
		"an unmatched ` then @pm":          {"pm"},
		"`open\n\n@pm` closes later":       {"pm"}, // code spans end at a blank line
		"`multi\nline @ceo` @user":         {"user"},
		"text\t@reviewer\ttab":             {"reviewer"},
		"@supervisor, @ceo; @developer?":   {"supervisor", "ceo", "developer"},
		"email first.last@user.example.io": nil,
	}
	for in, want := range cases {
		if got := ParseMentions(in); !slices.Equal(got, want) {
			t.Errorf("ParseMentions(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInvalidMentionPerTicketType(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	inStory := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})

	comment := func(n int, body string) error {
		_, err := b.Comment(ctx, User, int64(n), api.CommentRequest{Body: body})
		return err
	}
	wantCode(t, comment(story.Number, "@developer look"), api.CodeInvalidMention)
	wantCode(t, comment(alone.Number, "@pm look"), api.CodeInvalidMention)
	for _, ok := range []string{"@developer", "@reviewer", "@pm", "@supervisor", "@ceo", "@user"} {
		if err := comment(inStory.Number, ok+" look"); err != nil {
			t.Errorf("%s on a task in a story: %v", ok, err)
		}
	}
	for _, ok := range []string{"@reviewer", "@pm", "@supervisor", "@ceo", "@user"} {
		if err := comment(story.Number, ok); err != nil {
			t.Errorf("%s on a story: %v", ok, err)
		}
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	err := comment(alone.Number, "@ceo look")
	wantCode(t, err, api.CodeInvalidMention)
	// A failed comment stores nothing.
	cs, err := b.Comments(ctx, User, int64(alone.Number))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 0 {
		t.Fatalf("comments after invalid mentions = %+v", cs)
	}
}

func TestSupervisorMentionResolves(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
	pm := agent(t, b, api.RolePM, int64(story.Number))
	dev := agent(t, b, api.RoleDeveloper, int64(task.Number))

	res, err := b.Comment(ctx, dev, int64(task.Number), api.CommentRequest{Body: "@supervisor and @pm, see this"})
	if err != nil {
		t.Fatal(err)
	}
	want := []api.Mention{{Role: api.RolePM, Session: pm.Session}}
	if !slices.Equal(res.Comment.Mentions, want) {
		t.Fatalf("mentions = %+v, want %+v (pm once)", res.Comment.Mentions, want)
	}

	// A story the CEO supervises falls to the user while the CEO is off.
	ceoStory := mustCreate(t, b, agent(t, b, api.RoleCEO, 0), api.CreateTicket{Type: api.TypeStory, Title: "C", Projects: []string{"api"}})
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	res, err = b.Comment(ctx, User, int64(ceoStory.Number), api.CommentRequest{Body: "@supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []api.Mention{{Role: api.RoleUser}}; !slices.Equal(res.Comment.Mentions, want) {
		t.Fatalf("mentions = %+v, want %+v", res.Comment.Mentions, want)
	}
}

func TestMentionsInCodeAreIgnoredWhenStored(t *testing.T) {
	b, _ := newTestBoard(t)
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	// @pm is invalid on a standalone task, but inside code it is no mention.
	res, err := b.Comment(context.Background(), User, int64(alone.Number), api.CommentRequest{Body: "run `@pm`\n```\n@pm\n```"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Comment.Mentions) != 0 || res.Unreachable == nil || len(res.Unreachable) != 0 {
		t.Fatalf("result = %+v", res)
	}
}
