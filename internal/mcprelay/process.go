package mcprelay

import (
	"os/exec"

	"github.com/scolastico-dev/one-man-office/internal/processtree"
)

type processGroup struct{ *processtree.Group }

func newProcessGroup(cmd *exec.Cmd) (*processGroup, error) {
	g, err := processtree.New(cmd)
	if err != nil {
		return nil, err
	}
	return &processGroup{Group: g}, nil
}
func (g *processGroup) started(cmd *exec.Cmd) error { return g.Started(cmd) }
func (g *processGroup) close() {
	if g.Group != nil {
		g.Close()
	}
}
