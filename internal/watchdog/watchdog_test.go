package watchdog

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/kiro"
)

type fakeKiro struct {
	loggedIn   bool
	loginCalls int
}

func (f *fakeKiro) WhoAmI(context.Context) (bool, string, error) { return f.loggedIn, "", nil }
func (f *fakeKiro) Logout(context.Context) error                 { f.loggedIn = false; return nil }
func (f *fakeKiro) Login(_ context.Context, cb func(kiro.DeviceFlow) error) error {
	f.loginCalls++
	if err := cb(kiro.DeviceFlow{Code: "ABCD-EFGH", URL: "https://example.test/device?user_code=ABCD-EFGH"}); err != nil {
		return err
	}
	f.loggedIn = true
	return nil
}

type fakeNotifier struct{ messages []string }

func (f *fakeNotifier) Notify(_ context.Context, msg string) error {
	f.messages = append(f.messages, msg)
	return nil
}

func TestCheckLogsInAndNotifies(t *testing.T) {
	client := &fakeKiro{}
	n := &fakeNotifier{}
	cfg := config.Config{CheckInterval: time.Minute, NotifySuccess: true}
	w := New(cfg, client, n, log.New(io.Discard, "", 0))

	if err := w.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.loginCalls != 1 {
		t.Fatalf("login calls = %d", client.loginCalls)
	}
	if len(n.messages) != 2 {
		t.Fatalf("notifications = %d, want 2", len(n.messages))
	}
	if !strings.Contains(n.messages[0], "ABCD-EFGH") || !strings.Contains(n.messages[0], "https://example.test/device") {
		t.Fatalf("device notification missing code/url: %q", n.messages[0])
	}
}
