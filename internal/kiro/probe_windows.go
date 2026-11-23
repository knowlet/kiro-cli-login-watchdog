//go:build windows

package kiro

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	createSuspended                = 0x00000004
	jobObjectBasicLimitInformation = 2
	jobObjectLimitKillOnJobClose   = 0x00002000
	processSetQuota                = 0x0100
	processTerminate               = 0x0001
	threadSuspendResume            = 0x0002
)

var (
	modkernel32            = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW   = modkernel32.NewProc("CreateJobObjectW")
	procSetInformationJob  = modkernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob = modkernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject = modkernel32.NewProc("TerminateJobObject")
	procOpenThread         = modkernel32.NewProc("OpenThread")
	procResumeThread       = modkernel32.NewProc("ResumeThread")
	procThread32First      = modkernel32.NewProc("Thread32First")
	procThread32Next       = modkernel32.NewProc("Thread32Next")
)

type threadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePri        int32
	DeltaPri       int32
	Flags          uint32
}

type windowsProbeTree struct {
	cmd *exec.Cmd
	job syscall.Handle
}

func armProbeTree(cmd *exec.Cmd) (probeTree, error) {
	job, err := createKillJob()
	if err != nil {
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Stay suspended until the process is in the job, so every descendant
	// is created inside that job.
	cmd.SysProcAttr.CreationFlags |= createSuspended
	return &windowsProbeTree{cmd: cmd, job: job}, nil
}

func createKillJob() (syscall.Handle, error) {
	r, _, err := procCreateJobObjectW.Call(0, 0)
	if r == 0 {
		return 0, fmt.Errorf("create job object: %w", err)
	}
	job := syscall.Handle(r)
	info := jobBasicLimitBytes()
	ret, _, err := procSetInformationJob.Call(uintptr(job), jobObjectBasicLimitInformation,
		uintptr(unsafe.Pointer(&info[0])), uintptr(len(info)))
	if ret == 0 {
		_ = syscall.CloseHandle(job)
		return 0, fmt.Errorf("set job limits: %w", err)
	}
	return job, nil
}

func (t *windowsProbeTree) started() error {
	if t.cmd.Process == nil {
		return fmt.Errorf("probe process was not started")
	}
	handle, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(t.cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open probe process: %w", err)
	}
	defer syscall.CloseHandle(handle)
	r, _, err := procAssignProcessToJob.Call(uintptr(t.job), uintptr(handle))
	if r == 0 {
		return fmt.Errorf("assign probe to job: %w", err)
	}
	if err = resumeProcess(t.cmd.Process.Pid); err != nil {
		return fmt.Errorf("resume probe: %w", err)
	}
	return nil
}

func (t *windowsProbeTree) kill() {
	if t.job != 0 {
		_, _, _ = procTerminateJobObject.Call(uintptr(t.job), 1)
	}
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
}

func (t *windowsProbeTree) release() {
	if t.job != 0 {
		_ = syscall.CloseHandle(t.job)
		t.job = 0
	}
}

func resumeProcess(pid int) error {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(snapshot)
	var entry threadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	ret, _, err := procThread32First.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return err
	}
	for entry.OwnerProcessID != uint32(pid) {
		ret, _, err = procThread32Next.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			return err
		}
	}
	thread, _, err := procOpenThread.Call(threadSuspendResume, 0, uintptr(entry.ThreadID))
	if thread == 0 {
		return err
	}
	defer syscall.CloseHandle(syscall.Handle(thread))
	ret, _, err = procResumeThread.Call(thread)
	if ret == ^uintptr(0) {
		return err
	}
	return nil
}
