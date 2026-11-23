//go:build windows && (amd64 || arm64)

package kiro

import "unsafe"

// jobBasicLimitSize is sizeof(JOBOBJECT_BASIC_LIMIT_INFORMATION) on 64-bit Windows.
const jobBasicLimitSize = 64

func jobBasicLimitBytes() []byte {
	info := make([]byte, jobBasicLimitSize)
	// LimitFlags is the first DWORD after two LARGE_INTEGERs.
	*(*uint32)(unsafe.Pointer(&info[16])) = jobObjectLimitKillOnJobClose
	return info
}
