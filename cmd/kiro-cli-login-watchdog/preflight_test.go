package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsupportedKASFailsBeforeDaemonStartup(t *testing.T) {
	for _, command := range []string{"once", "run", "start"} {
		t.Run(command, func(t *testing.T) {
			exe, dir := helperEnvironment(t, "kas-unsupported")
			t.Setenv("KCLW_BACKEND", "kas")
			t.Setenv("KCLW_KIROCREW_BIN", exe)
			t.Setenv("KCLW_KIRO_BIN", "must-not-resolve-kiro-cli")
			out, err := invoke(exe, command)
			if err == nil || !strings.Contains(out, "v0.10.0 lacks these commands") {
				t.Fatalf("command=%s err=%v output=%q", command, err, out)
			}
			for _, file := range []string{"watchdog.pid", "watchdog.log", "watchdog.pid.lock", "watchdog.pid.control.lock"} {
				if _, err := os.Stat(filepath.Join(dir, file)); !os.IsNotExist(err) {
					t.Fatalf("preflight created %s: %v", file, err)
				}
			}
			// Status and stop do not depend on the auth CLI being installed/compatible.
			t.Setenv("KCLW_KIROCREW_BIN", "must-not-resolve-kirocrew")
			if out := requireCommand(t, exe, "status"); strings.TrimSpace(out) != "stopped" {
				t.Fatal(out)
			}
			if out, err := invoke(exe, "stop"); err == nil || !strings.Contains(out, "not running") {
				t.Fatalf("stop: %v %s", err, out)
			}
		})
	}
}

func TestKASPreflightFailureAndTimeout(t *testing.T) {
	for _, mode := range []string{"kas-help-error", "kas-help-wait"} {
		t.Run(mode, func(t *testing.T) {
			exe, _ := helperEnvironment(t, mode)
			t.Setenv("KCLW_BACKEND", "kas")
			t.Setenv("KCLW_KIROCREW_BIN", exe)
			t.Setenv("KCLW_COMMAND_TIMEOUT", "100ms")
			out, err := invoke(exe, "once")
			if err == nil || !strings.Contains(out, "probe kirocrew kas --help") {
				t.Fatalf("err=%v output=%q", err, out)
			}
			if strings.Contains(out, "private-help-diagnostic") {
				t.Fatal("raw subprocess output leaked")
			}
		})
	}
}
