package watchdog

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
)

func TestKASNotificationsAndProactiveRelogin(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "signed-out"
		if force {
			name = "proactive"
		}
		t.Run(name, func(t *testing.T) {
			client := &fakeKiro{loggedIn: force}
			n := &fakeNotifier{}
			var logs bytes.Buffer
			cfg := config.Config{Backend: config.BackendKAS, NotifySuccess: true}
			if force {
				cfg.ForceReloginInterval = time.Hour
			}
			w := New(cfg, client, n, log.New(&logs, "", 0))
			w.lastRelogin = time.Now().Add(-2 * time.Hour)
			if err := w.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			if client.loginCalls != 1 || len(n.messages) != 2 {
				t.Fatalf("logins=%d notifications=%d", client.loginCalls, len(n.messages))
			}
			for _, msg := range n.messages {
				if !strings.Contains(msg, "KiroCrew KAS") || strings.Contains(msg, "Kiro CLI") {
					t.Fatalf("wrong backend label: %s", msg)
				}
			}
			if strings.Contains(logs.String(), "ABCD-EFGH") || strings.Contains(logs.String(), "https://") {
				t.Fatal("credentials leaked into scheduler logs")
			}
			if err := w.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			if client.loginCalls != 1 || len(n.messages) != 2 {
				t.Fatal("healthy KAS triggered another login")
			}
		})
	}
}
