package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackendLoad(t *testing.T) {
	for _, tc := range []struct{ backend, want, key, bin string }{
		{"", BackendKiroCLI, "KCLW_KIRO_BIN", "kiro-cli"},
		{"kiro-cli", BackendKiroCLI, "KCLW_KIRO_BIN", "kiro-cli"},
		{" KAS ", BackendKAS, "KCLW_KIROCREW_BIN", "kirocrew"},
	} {
		t.Run(tc.want+tc.backend, func(t *testing.T) {
			for _, key := range []string{"KCLW_KIRO_BIN", "KCLW_KIROCREW_BIN", "KCLW_CHECK_INTERVAL", "KCLW_FORCE_RELOGIN_INTERVAL", "KCLW_COMMAND_TIMEOUT", "KCLW_LOGIN_TIMEOUT", "KCLW_NOTIFY_SUCCESS", "KCLW_PID_FILE", "KCLW_LOG_FILE"} {
				t.Setenv(key, "")
			}
			t.Setenv("KCLW_STATE_DIR", t.TempDir())
			t.Setenv("KCLW_BACKEND", tc.backend)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			bin, key := cfg.Executable()
			if cfg.Backend != tc.want || bin != tc.bin || key != tc.key {
				t.Fatalf("backend=%q executable=%q key=%q", cfg.Backend, bin, key)
			}
			custom := filepath.Join(t.TempDir(), "program with spaces")
			t.Setenv(tc.key, custom)
			cfg, err = Load()
			if err != nil {
				t.Fatal(err)
			}
			if bin, _ = cfg.Executable(); bin != custom {
				t.Fatalf("executable = %q", bin)
			}
		})
	}
}

func TestBackendValidation(t *testing.T) {
	base := Config{KiroBin: "kiro-cli", KiroCrewBin: "kirocrew", AuthMethod: AuthBuilderID,
		CheckInterval: time.Minute, CommandTimeout: time.Second, LoginTimeout: time.Minute,
		TelegramBotToken: "test-placeholder", TelegramChatID: "0"}
	for _, backend := range []string{"", BackendKiroCLI, BackendKAS} {
		for _, method := range []string{AuthBuilderID, AuthIdentityCenter, AuthGoogle, AuthGitHub} {
			t.Run(backend+"/"+method, func(t *testing.T) {
				cfg := base
				cfg.Backend = backend
				cfg.AuthMethod = method
				cfg.IdentityProvider = "https://example.awsapps.com/start"
				cfg.Region = "ap-northeast-1"
				if backend == BackendKAS {
					cfg.KiroBin = ""
				} else {
					cfg.KiroCrewBin = ""
				}
				if err := cfg.ValidateRuntime(); err != nil {
					t.Fatal(err)
				}
				if method == AuthIdentityCenter {
					cfg.Region = ""
					if err := cfg.ValidateRuntime(); err == nil {
						t.Fatal("accepted missing IdC region")
					}
					cfg.Region = "ap-northeast-1"
					cfg.IdentityProvider = ""
					if err := cfg.ValidateRuntime(); err == nil {
						t.Fatal("accepted missing IdC start URL")
					}
				}
			})
		}
	}
	for _, tc := range []struct{ backend, cli, crew, want string }{
		{"typo", "kiro-cli", "kirocrew", "KCLW_BACKEND"},
		{BackendKAS, "kiro-cli", "", "KCLW_KIROCREW_BIN"},
		{BackendKiroCLI, "", "kirocrew", "KCLW_KIRO_BIN"},
	} {
		cfg := base
		cfg.Backend = tc.backend
		cfg.KiroBin = tc.cli
		cfg.KiroCrewBin = tc.crew
		if err := cfg.ValidateRuntime(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("case %+v: %v", tc, err)
		}
	}
}
