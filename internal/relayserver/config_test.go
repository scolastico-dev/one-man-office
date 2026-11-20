package relayserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordFileValidation(t *testing.T) {
	for _, secret := range []string{"", "\n", "one\ntwo", "one\x00two", strings.Repeat("a", 4097)} {
		path := filepath.Join(t.TempDir(), "password")
		if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(Config{PasswordFile: path}); err == nil {
			t.Fatal("accepted invalid password file")
		}
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		if _, err := New(Config{PasswordFile: path}); err == nil {
			t.Fatal("accepted unreadable password file")
		}
	}
}

func TestAdmissionBeforeDispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	s, _ := New(Config{PasswordFile: path})
	defer s.Close()
	for _, route := range []string{"/v1/ws", "/notifications/office/" + strings.Repeat("a", 96)} {
		for _, credentials := range [][2]string{{"wrong-user", "secret"}, {"relay", "wrong"}} {
			r := httptest.NewRequest("GET", route, nil)
			r.SetBasicAuth(credentials[0], credentials[1])
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatal(w.Code)
			}
		}
	}
}
