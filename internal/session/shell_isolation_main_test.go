package session

import (
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

func TestMain(m *testing.M) {
	// The priority probe is an intentionally configured child, like a fake
	// agent. Its parent has already isolated the inherited environment.
	if os.Getenv("OMO_PRIORITY_HELPER") == "1" {
		os.Exit(m.Run())
	}
	os.Exit(childenvtest.Run(m))
}
