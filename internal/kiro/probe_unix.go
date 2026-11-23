//go:build !windows

package kiro

import (
	"os/exec"
	"syscall"
)

type unixProbeTree struct{ cmd *exec.Cmd }

func armProbeTree(cmd *exec.Cmd) (probeTree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// The child becomes its own process-group leader before exec, so later
	// descendants share that group and a single signal reaches all of them.
	cmd.SysProcAttr.Setpgid = true
	return &unixProbeTree{cmd: cmd}, nil
}

func (t *unixProbeTree) started() error { return nil }

func (t *unixProbeTree) kill() {
	if t.cmd.Process == nil || t.cmd.Process.Pid <= 0 {
		return
	}
	_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
}

func (t *unixProbeTree) release() {}
