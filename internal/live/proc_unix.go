//go:build unix

package live

import (
	"errors"
	"os/exec"
	"syscall"
)

// newSession : le scénario dans sa propre session, pour tuer tout le groupe.
func newSession(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

// exitCode : code de sortie façon Python (signal -> -N).
func exitCode(cmd *exec.Cmd, err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return -int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}
