package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
)

// Re-exec the test binary: exercise argv, pipes and cancellation on every CI OS
// without KiroCrew, a shell, network access or a real credential store.
func runKASHelper(args []string) int {
	if len(args) < 2 || args[0] != "kas" {
		return 90
	}
	if raw := os.Getenv("KCLW_KAS_EXPECT"); raw != "" {
		var want []string
		if json.Unmarshal([]byte(raw), &want) != nil || !reflect.DeepEqual(args, want) {
			return 91
		}
	}
	if want := os.Getenv("KCLW_KAS_HOME"); want != "" && os.Getenv("KIROCREW_HOME") != want {
		return 92
	}
	mode := os.Getenv("KCLW_KAS_CASE")
	switch args[1] {
	case "whoami", "logout":
		switch mode {
		case "signed-out":
			fmt.Fprintln(os.Stderr, "Not signed in to Kiro Crew.")
			return 1
		case "ansi-signed-out":
			fmt.Fprintln(os.Stderr, "\x1b[31mNot signed in to Kiro Crew.\x1b[0m")
			return 1
		case "legacy":
			fmt.Fprintln(os.Stderr, "Not logged in")
			return 1
		case "traceback":
			fmt.Fprintln(os.Stderr, "Traceback: vault unavailable")
			return 1
		case "mixed":
			fmt.Fprintln(os.Stderr, "Not signed in to Kiro Crew.\nConnection failed")
			return 1
		case "wrong-exit":
			fmt.Fprintln(os.Stderr, "Not signed in to Kiro Crew.")
			return 2
		case "flags":
			fmt.Fprintln(os.Stderr, "usage: kirocrew: invalid choice: 'whoami'")
			return 2
		case "empty":
			return 1
		case "wait":
			time.Sleep(time.Minute)
		}
		fmt.Println("Signed in to Kiro Crew: BuilderId (builder_id)")
		return 0
	case "login":
		// The exact upstream prompt, including a URL that has neither /device nor
		// user_code. Its code arrives on the following line.
		fmt.Println("Open https://example.test/activate in a browser on any device")
		fmt.Println("code: ABCD-EFGH")
		fmt.Println("Waiting for approval; this prompt expires at 2026-10-27T12:00:00Z ...")
		if mode == "wait" {
			time.Sleep(time.Minute)
		}
		return 0
	}
	return 93
}

func kasClient(t *testing.T, mode string, logger *log.Logger) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KCLW_KAS_HELPER", "1")
	t.Setenv("KCLW_KAS_CASE", mode)
	t.Setenv("KCLW_KAS_EXPECT", "")
	t.Setenv("KCLW_KAS_HOME", "")
	return New(config.Config{Backend: config.BackendKAS, KiroCrewBin: exe,
		KiroBin: "must-not-run-kiro-cli", AuthMethod: config.AuthBuilderID,
		CommandTimeout: 3 * time.Second, LoginTimeout: 5 * time.Second}, logger)
}

func expectKASArgs(t *testing.T, args ...string) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KCLW_KAS_EXPECT", string(raw))
}

func TestKASLoginArguments(t *testing.T) {
	url := "https://example.awsapps.com/start; echo $(whoami) & ignored"
	for _, tc := range []struct {
		method string
		args   []string
	}{
		{config.AuthIdentityCenter, []string{"kas", "login", "--provider", "idc", "--start-url", url, "--region", "ap-northeast-1"}},
		{config.AuthBuilderID, []string{"kas", "login", "--provider", "builder_id"}},
		{config.AuthGoogle, []string{"kas", "login", "--provider", "google"}},
		{config.AuthGitHub, []string{"kas", "login", "--provider", "github"}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			c := kasClient(t, "healthy", nil)
			c.authMethod = tc.method
			c.identityURL = url
			c.region = "ap-northeast-1"
			expectKASArgs(t, tc.args...)
			if err := c.Login(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKASWhoAmIClassification(t *testing.T) {
	for _, tc := range []struct {
		mode             string
		healthy, wantErr bool
	}{
		{"healthy", true, false}, {"signed-out", false, false}, {"ansi-signed-out", false, false},
		{"legacy", false, true}, {"traceback", false, true}, {"mixed", false, true},
		{"wrong-exit", false, true}, {"flags", false, true}, {"empty", false, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			c := kasClient(t, tc.mode, nil)
			expectKASArgs(t, "kas", "whoami")
			healthy, _, err := c.WhoAmI(context.Background())
			if healthy != tc.healthy || (err != nil) != tc.wantErr {
				t.Fatalf("healthy=%v err=%v", healthy, err)
			}
			if err != nil && !strings.Contains(err.Error(), "kirocrew kas whoami") {
				t.Fatal(err)
			}
		})
	}
	t.Run("missing executable does not fall back", func(t *testing.T) {
		c := kasClient(t, "healthy", nil)
		c.bin += " ; echo injected"
		if ok, _, err := c.WhoAmI(context.Background()); ok || err == nil {
			t.Fatalf("healthy=%v err=%v", ok, err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		c := kasClient(t, "wait", nil)
		c.commandTimeout = 50 * time.Millisecond
		if _, _, err := c.WhoAmI(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		c := kasClient(t, "healthy", nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := c.WhoAmI(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestKASLogoutAndInheritedHome(t *testing.T) {
	c := kasClient(t, "healthy", nil)
	home := t.TempDir()
	t.Setenv("KIROCREW_HOME", home)
	t.Setenv("KCLW_KAS_HOME", home)
	expectKASArgs(t, "kas", "logout")
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KCLW_KAS_CASE", "traceback")
	if err := c.Logout(context.Background()); err == nil {
		t.Fatal("logout failure ignored")
	}
}

func TestKASDeviceFlowAndRedaction(t *testing.T) {
	var logs bytes.Buffer
	c := kasClient(t, "healthy", log.New(&logs, "", 0))
	calls := 0
	if err := c.Login(context.Background(), func(flow DeviceFlow) error {
		calls++
		if flow.Code != "ABCD-EFGH" || flow.URL != "https://example.test/activate" {
			return fmt.Errorf("unexpected flow: %+v", flow)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("callbacks=%d", calls)
	}
	if strings.Contains(logs.String(), "ABCD") || strings.Contains(logs.String(), "https://") {
		t.Fatalf("credential leaked: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "kirocrew kas:") {
		t.Fatal("missing KAS log label")
	}
}

func TestKASLoginCancellation(t *testing.T) {
	t.Run("notification failure", func(t *testing.T) {
		c := kasClient(t, "wait", nil)
		sentinel := errors.New("notification unavailable")
		if err := c.Login(context.Background(), func(DeviceFlow) error { return sentinel }); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		c := kasClient(t, "wait", nil)
		c.loginTimeout = 80 * time.Millisecond
		if err := c.Login(context.Background(), nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
}

func TestKASPromptCueIsSpecific(t *testing.T) {
	for _, line := range []string{
		"Documentation: https://example.test/activate",
		"Open https://example.test/activate for documentation",
		"Could not open https://example.test/activate",
	} {
		if got := deviceURLCandidates(line); len(got) != 0 {
			t.Fatalf("accepted non-prompt %q", line)
		}
	}
}
