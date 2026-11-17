package luabox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactorClean(t *testing.T) {
	r := NewRedactor()
	r.Add("short")               // under 6 bytes: ignored
	r.Add("abcdef")              // contained in the longer one
	r.Add("abcdefghijkl-secret") // longest first
	got := r.Clean("x short abcdefghijkl-secret abcdef\n\u202e")
	want := `x short [redacted] [redacted]\u{A}\u{202E}`
	if got != want {
		t.Fatalf("Clean = %q, want %q", got, want)
	}
	var nilRed *Redactor
	if nilRed.Clean("a\tb") != `a\u{9}b` {
		t.Fatal("nil redactor does not escape")
	}
}

func TestRedactorCleansErrorsAndLogs(t *testing.T) {
	const fileText = `{"token":"tok-from-file-123","nested":{"refresh":"refresh-value-456"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	b := New(context.Background(), Limits{})
	defer b.Close()
	b.Set("read_file", func(L *luaState) int {
		b.Redactor().MarkSource(fileText)
		L.Push(luaString(fileText))
		return 1
	})
	h := &HTTP{Client: srv.Client()}
	b.Set("http", h.Func(b))
	err := b.Load(`
		omo.log("early tok-from-file-123 and refresh-value-456")
		local raw = omo.read_file("x")
		omo.log("raw " .. raw)
		local data = omo.json_decode(raw)
		omo.log("header Bearer header-secret-789 sent")
		local res, e = omo.http({url = "`+srv.URL+`", headers = {Authorization = "Bearer header-secret-789", ["X-Key"] = "x-key-value-000"}})
		assert(res, e)
		omo.log("key x-key-value-000")
		error("failed with " .. data.token .. " " .. data.nested.refresh .. " x-key-value-000 header-secret-789")
	`, "redact")
	if err == nil {
		t.Fatal("script did not fail")
	}
	texts := append(b.Log(), err.Error(), ErrorText(err, b.Redactor()))
	for _, secret := range []string{"tok-from-file-123", "refresh-value-456", "header-secret-789", "x-key-value-000", fileText} {
		for _, text := range texts {
			if strings.Contains(text, secret) {
				t.Errorf("%q holds %q", text, secret)
			}
		}
	}
	if len(b.Log()) != 4 || !strings.HasPrefix(b.Log()[0], "early [redacted] and [redacted]") {
		t.Fatalf("log %q", b.Log())
	}
	if !strings.HasPrefix(err.Error(), "line 10: failed with [redacted]") {
		t.Fatalf("error %q", err.Error())
	}
}

func TestDecodedStringsOfUnmarkedTextAreNotSecrets(t *testing.T) {
	b := New(context.Background(), Limits{})
	defer b.Close()
	if err := b.Load(`omo.log(omo.json_decode('{"a":"visible-value"}').a)`, "x"); err != nil {
		t.Fatal(err)
	}
	if b.Log()[0] != "visible-value" {
		t.Fatalf("log %q", b.Log())
	}
}

func TestErrorTextCut(t *testing.T) {
	r := NewRedactor()
	got := ErrorText(&ScriptError{Line: 4, msg: strings.Repeat("é", 400)}, r)
	if n := len([]rune(got)); n != 300 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "line 4: ") {
		t.Fatalf("%d runes: %q", n, got)
	}
	if ErrorText(nil, r) != "" {
		t.Fatal("nil error")
	}
}
