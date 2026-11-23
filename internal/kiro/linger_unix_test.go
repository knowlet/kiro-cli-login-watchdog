//go:build !windows

package kiro

import (
	"os/exec"
	"syscall"
)

func detachLinger(*exec.Cmd) {}

func descendantExited(pid int) bool {
	return syscall.Kill(pid, 0) != nil
}
