//go:build unix

package probes

import (
	"os/exec"
	"syscall"
)

// setPgid : la sonde et ses enfants dans leur propre groupe, tués ensemble au timeout.
func setPgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
