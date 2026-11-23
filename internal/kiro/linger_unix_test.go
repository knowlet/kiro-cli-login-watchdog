//go:build !windows

package kiro

import "os/exec"

func detachLinger(*exec.Cmd) {}
