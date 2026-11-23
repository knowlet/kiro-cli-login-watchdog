//go:build windows

package kiro

import (
	"os/exec"
	"syscall"
)

func detachLinger(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func descendantExited(pid int) bool {
	const stillActive = 259
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return true
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err = syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code != stillActive
}
