package e2e

import (
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

func TestEnvironmentDropsInheritedOmo(t *testing.T) {
	childenvtest.SetOmo(t)
	clearOmoEnvironment()
	childenvtest.CheckOmo(t, os.Environ(), nil)
}
