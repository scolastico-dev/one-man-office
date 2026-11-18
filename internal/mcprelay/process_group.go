package mcprelay

import (
	"errors"
	"os/exec"
)

// commandGroup is the lifecycle of a command's OS process group/job.
type commandGroup interface {
	started(*exec.Cmd) error
	close()
}

// startGrouped never leaves a started but unsupervised/suspended command
// behind. Both stdio servers and header commands use this failure policy.
func startGrouped(cmd *exec.Cmd, group commandGroup) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := group.started(cmd); err != nil {
		group.close()
		_ = cmd.Wait()
		return errors.New("cannot supervise the process")
	}
	return nil
}
