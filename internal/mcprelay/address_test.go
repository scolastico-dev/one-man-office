package mcprelay

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// lockedBuffer is omo's log in a test.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The secrets of a server's address (a key in its query, a password) never
// reach omo's log, the server's log, an event, a state, a test result or
// an error an agent or the user gets, when the server cannot be reached.
func TestAddressSecretsAreNeverShown(t *testing.T) {
	const canary = "canary-key-0f9e8d7c"
	closed := httptest.NewServer(http.NotFoundHandler())
	addr := closed.URL + "/mcp?api_key=" + canary + "&plain=" + canary + "%2B"
	closed.Close()

	plain := httpServer("keyed", addr)
	oauth := oauthServer("signed", addr)
	f := newFixture(t, func(t *tunables) { t.maxFailures = 1 }, plain, oauth)
	omoLog := &lockedBuffer{}
	f.pool.o.Log = log.New(omoLog, "", 0)

	var texts []string
	res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "keyed__echo", nil)
	texts = append(texts, resultText(res))
	tr, err := f.pool.Test(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}
	texts = append(texts, tr.Error)
	if _, err := f.pool.Connect(context.Background(), "signed"); err == nil {
		t.Fatal("connect to a closed address succeeded")
	} else {
		texts = append(texts, err.Error())
	}
	eventually(t, "the failure in omo's log", func() bool { return strings.Contains(omoLog.String(), "keyed") })
	raw, _ := json.Marshal(f.pool.Status())
	all := strings.Join(texts, "\n") + "\n" + omoLog.String() + f.logText("keyed") + f.logText("signed") + f.events.all() + string(raw)
	if !strings.Contains(all, "api_key=***") {
		t.Fatalf("the address is not shown masked:\n%s", all)
	}
	if strings.Contains(all, canary) {
		t.Fatalf("a secret of the address was shown:\n%s", all)
	}
}
