//go:build windows

package kiro

import (
	"os/exec"
	"syscall"
)

func detachLinger(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
