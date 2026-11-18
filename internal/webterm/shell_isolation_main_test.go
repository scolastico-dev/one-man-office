package webterm

import (
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

func TestMain(m *testing.M) { os.Exit(childenvtest.Run(m)) }
