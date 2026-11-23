//go:build windows && (amd64 || arm64)

package kiro

import (
	"fmt"
	"syscall"
	"unsafe"
)

type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobExtendedLimit struct {
	Basic                 jobBasicLimit
	Io                    ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func setJobKillOnClose(job syscall.Handle) error {
	info := jobExtendedLimit{}
	info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
	ret, _, err := procSetInformationJob.Call(uintptr(job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if ret == 0 {
		return fmt.Errorf("set job limits: %w", err)
	}
	return nil
}
