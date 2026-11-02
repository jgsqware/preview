//go:build !unix

package live

import (
	"errors"
	"os/exec"
)

func newSession(cmd *exec.Cmd) {}

func killGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func exitCode(cmd *exec.Cmd, err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}
