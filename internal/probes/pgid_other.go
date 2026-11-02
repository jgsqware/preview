//go:build !unix

package probes

import "os/exec"

func setPgid(cmd *exec.Cmd) {}
