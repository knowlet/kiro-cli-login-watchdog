//go:build windows && (386 || arm)

package kiro

import "unsafe"

// jobBasicLimitSize is sizeof(JOBOBJECT_BASIC_LIMIT_INFORMATION) on 32-bit Windows,
// including the trailing pad to an 8-byte boundary.
const jobBasicLimitSize = 48

func jobBasicLimitBytes() []byte {
	info := make([]byte, jobBasicLimitSize)
	*(*uint32)(unsafe.Pointer(&info[16])) = jobObjectLimitKillOnJobClose
	return info
}
