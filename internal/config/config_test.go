package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadExplicitStateDirWithoutHome(t *testing.T) {
	stateDir := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "AppData", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "KCLW_PID_FILE", "KCLW_LOG_FILE"} {
		t.Setenv(key, "")
	}
	t.Setenv("KCLW_STATE_DIR", stateDir)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with explicit state directory: %v", err)
	}
	if cfg.StateDir != stateDir {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, stateDir)
	}
	if want := filepath.Join(stateDir, "watchdog.pid"); cfg.PIDFile != want {
		t.Errorf("PIDFile = %q, want %q", cfg.PIDFile, want)
	}
	if want := filepath.Join(stateDir, "watchdog.log"); cfg.LogFile != want {
		t.Errorf("LogFile = %q, want %q", cfg.LogFile, want)
	}
}

func TestLoadStateDirectoryPaths(t *testing.T) {
	home := t.TempDir()
	explicit := filepath.Join(home, "explicit")
	appData := filepath.Join(home, "app-data")
	xdgState := filepath.Join(home, "xdg-state")
	defaultDir := filepath.Join(xdgState, "kiro-cli-login-watchdog")
	switch runtime.GOOS {
	case "darwin":
		defaultDir = filepath.Join(home, "Library", "Application Support", "kiro-cli-login-watchdog")
	case "windows":
		defaultDir = filepath.Join(appData, "kiro-cli-login-watchdog")
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(key, home)
	}
	t.Setenv("AppData", appData)
	t.Setenv("XDG_STATE_HOME", xdgState)
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, tt := range []struct {
		name, value, want string
	}{
		{"explicit", explicit, explicit},
		{"trimmed explicit", "  " + explicit + "  ", explicit},
		{"home expansion", "~/explicit", explicit},
		{"platform default", "", defaultDir},
		{"whitespace default", "  ", defaultDir},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KCLW_STATE_DIR", tt.value)
			pidFile := filepath.Join(home, "custom.pid")
			logFile := filepath.Join(home, "custom.log")
			t.Setenv("KCLW_PID_FILE", pidFile)
			t.Setenv("KCLW_LOG_FILE", logFile)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.StateDir != tt.want {
				t.Errorf("StateDir = %q, want %q", cfg.StateDir, tt.want)
			}
			if cfg.PIDFile != pidFile || cfg.LogFile != logFile {
				t.Errorf("explicit file paths changed: PIDFile = %q, LogFile = %q", cfg.PIDFile, cfg.LogFile)
			}
		})
	}
}

func TestLoadWithoutStateDirectoryOrHomeFails(t *testing.T) {
	for _, key := range []string{"HOME", "USERPROFILE", "AppData", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "KCLW_STATE_DIR"} {
		t.Setenv(key, "")
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load without an explicit or default state directory succeeded")
	}
}
