//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	detachedProcess                       = 0x00000008
	createNewProcessGroup                 = 0x00000200
	lockfileFailImmediately               = 0x00000001
	lockfileExclusiveLock                 = 0x00000002
	errorLockViolation      syscall.Errno = 33
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true,
		CreationFlags: detachedProcess | createNewProcessGroup}
}

func lockFile(file *os.File) error {
	var overlapped syscall.Overlapped
	ok, _, err := lockFileEx.Call(file.Fd(), lockfileFailImmediately|lockfileExclusiveLock,
		0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	runtime.KeepAlive(file)
	if ok != 0 {
		return nil
	}
	if err == errorLockViolation {
		return ErrLocked
	}
	return err
}
