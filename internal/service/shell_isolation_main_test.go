package service

import (
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

func TestMain(m *testing.M) {
	if detachedFixture() {
		return
	}
	os.Exit(childenvtest.Run(m))
}
