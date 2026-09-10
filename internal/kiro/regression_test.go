package kiro

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
)

// Re-exec the test executable instead of requiring a shell or a real Kiro
// installation. These fixtures run identically in the three native CI jobs.
func TestMain(m *testing.M) {
	if os.Getenv("KCLW_KIRO_HELPER") == "1" && len(os.Args) > 1 {
		switch os.Args[1] {
		case "whoami", "logout":
			switch os.Getenv("KCLW_KIRO_CASE") {
			case "logged-out":
				fmt.Fprintln(os.Stderr, "error: Not logged in")
				os.Exit(1)
			case "json-logged-out":
				fmt.Fprintln(os.Stderr, `{"error":"Not logged in"}`)
				os.Exit(1)
			case "service":
				fmt.Fprintln(os.Stderr, "service unavailable")
				os.Exit(1)
			case "flags":
				fmt.Fprintln(os.Stderr, "unexpected argument '--format'")
				os.Exit(2)
			case "empty":
				os.Exit(1)
			case "timeout":
				time.Sleep(time.Minute)
				os.Exit(0)
			default:
				fmt.Fprintln(os.Stdout, `{"authenticated":true}`)
				os.Exit(0)
			}
		case "login":
			switch os.Getenv("KCLW_KIRO_CASE") {
			case "long":
				fmt.Print(strings.Repeat("x", maxLoginLine+1))
				time.Sleep(time.Minute)
			case "wait":
				fmt.Println("Visit https://example.test/#/device?user_code=ABCD-EFGH")
				time.Sleep(time.Minute)
			case "argv":
				if len(os.Args) != 9 || os.Args[5] != os.Getenv("KCLW_EXPECT_ARG") {
					os.Exit(3)
				}
			default:
				// Exit immediately after emitting both streams, with an unterminated
				// final line: the former Wait-before-scanners implementation lost data.
				fmt.Fprintln(os.Stderr, "Code : ABCD-EFGH")
				fmt.Fprint(os.Stdout, "Visit https://example.test/#/device?user_code=ABCD-EFGH")
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func helperClient(t *testing.T, mode string, logger *log.Logger) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KCLW_KIRO_HELPER", "1")
	t.Setenv("KCLW_KIRO_CASE", mode)
	return New(config.Config{KiroBin: exe, CommandTimeout: 3 * time.Second,
		LoginTimeout: 5 * time.Second, AuthMethod: config.AuthBuilderID}, logger)
}

func TestWhoAmIFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		mode             string
		healthy, wantErr bool
	}{
		{"healthy", true, false}, {"logged-out", false, false}, {"json-logged-out", false, false},
		{"service", false, true}, {"flags", false, true}, {"empty", false, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			c := helperClient(t, tc.mode, nil)
			healthy, _, err := c.WhoAmI(context.Background())
			if healthy != tc.healthy || (err != nil) != tc.wantErr {
				t.Fatalf("healthy=%v err=%v", healthy, err)
			}
		})
	}
	t.Run("missing executable", func(t *testing.T) {
		c := helperClient(t, "healthy", nil)
		c.bin = t.TempDir() + "/missing-kiro"
		if healthy, _, err := c.WhoAmI(context.Background()); healthy || err == nil {
			t.Fatal("spawn failure was treated as logged out")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		c := helperClient(t, "timeout", nil)
		c.commandTimeout = 50 * time.Millisecond
		_, _, err := c.WhoAmI(context.Background())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		c := helperClient(t, "healthy", nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := c.WhoAmI(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestLoginDrainsBothStreamsAndRedacts(t *testing.T) {
	var logs bytes.Buffer
	c := helperClient(t, "flow", log.New(&logs, "", 0))
	for n := 0; n < 12; n++ {
		calls := 0
		if err := c.Login(context.Background(), func(flow DeviceFlow) error {
			calls++
			if flow.Code != "ABCD-EFGH" || !strings.Contains(flow.URL, "#/device?") {
				return errors.New("incorrect flow")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("iteration %d: calls=%d", n, calls)
		}
	}
	if strings.Contains(logs.String(), "ABCD-EFGH") || strings.Contains(logs.String(), "https://") {
		t.Fatal("credentials leaked into logs")
	}
}

func TestLoginCallbackFailureAndOutputLimit(t *testing.T) {
	t.Run("callback cancels child", func(t *testing.T) {
		c := helperClient(t, "wait", nil)
		sentinel := errors.New("notifier unavailable")
		if err := c.Login(context.Background(), func(DeviceFlow) error { return sentinel }); !errors.Is(err, sentinel) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("long line fails promptly", func(t *testing.T) {
		c := helperClient(t, "long", log.New(io.Discard, "", 0))
		err := c.Login(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("deadline cancels child", func(t *testing.T) {
		c := helperClient(t, "wait", nil)
		c.loginTimeout = 80 * time.Millisecond
		if err := c.Login(context.Background(), nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestLoginArgumentsAreNotShellInterpreted(t *testing.T) {
	c := helperClient(t, "argv", nil)
	c.authMethod = config.AuthIdentityCenter
	c.region = "ap-northeast-1"
	c.identityURL = `https://example.test/start; echo injected $(whoami) & ignored`
	t.Setenv("KCLW_EXPECT_ARG", c.identityURL)
	if err := c.Login(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	c.bin += " ; echo injected"
	if _, _, err := c.WhoAmI(context.Background()); err == nil {
		t.Fatal("a command string was executed")
	}
}

func TestParserAndRedactorRecognizeSameVariants(t *testing.T) {
	for _, line := range []string{
		"Visit https://example.test/#/device?user_code=ABCD-EFGH",
		"https://example.test/activate?user_code=ABCD-EFGH",
		"Docs https://kiro.dev/docs then https://example.test/#/device?user_code=ABCD%2DEFGH",
		"\x1b[32mCode : ABCD-EFGH\x1b[0m",
	} {
		t.Run(line, func(t *testing.T) {
			var got DeviceFlow
			p := NewDeviceFlowParser(func(flow DeviceFlow) error { got = flow; return nil })
			p.Feed(line)
			if strings.Contains(line, "Code :") {
				p.Feed("Visit https://example.test/device")
			}
			if got.Code != "ABCD-EFGH" {
				t.Fatalf("flow=%+v", got)
			}
			clean := sanitizeLogLine(line)
			if strings.Contains(clean, "ABCD") || strings.Contains(clean, "https://") {
				t.Fatalf("not redacted: %s", clean)
			}
		})
	}
}

func TestParserConcurrentCallbackExactlyOnce(t *testing.T) {
	calls := 0
	p := NewDeviceFlowParser(func(DeviceFlow) error { calls++; return nil })
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.Feed("Visit https://example.test/#/device?user_code=ABCD-EFGH") }()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
