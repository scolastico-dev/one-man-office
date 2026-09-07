//go:build windows

package sockc

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/scolastico-dev/one-man-office/internal/proto"
)

func TestCallWithoutDeadlineStillConnectsToWindowsPipe(t *testing.T) {
	path := fmt.Sprintf(`\\.\pipe\omo-call-%d-%d`, os.Getpid(), time.Now().UnixNano())
	listener, err := winio.ListenPipe(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request proto.Request
		if json.NewDecoder(conn).Decode(&request) == nil {
			_ = json.NewEncoder(conn).Encode(proto.Response{OK: true})
		}
	}()
	if err := Call(path, "user", "office.estop", nil, nil); err != nil {
		t.Fatal(err)
	}
}
