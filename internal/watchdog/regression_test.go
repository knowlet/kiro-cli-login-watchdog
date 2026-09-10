package watchdog

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/kiro"
)

type failingHealthCheck struct {
	calls int
	err   error
}

func (f *failingHealthCheck) WhoAmI(context.Context) (bool, string, error) { return false, "", f.err }
func (f *failingHealthCheck) Logout(context.Context) error                 { f.calls++; return nil }
func (f *failingHealthCheck) Login(context.Context, func(kiro.DeviceFlow) error) error {
	f.calls++
	return nil
}

func TestHealthCheckErrorDoesNotTriggerLoginOrLogout(t *testing.T) {
	sentinel := errors.New("service unavailable")
	client := &failingHealthCheck{err: sentinel}
	n := &fakeNotifier{}
	w := New(config.Config{ForceReloginInterval: time.Nanosecond}, client, n, log.New(io.Discard, "", 0))
	w.lastRelogin = time.Now().Add(-time.Hour)
	if err := w.Check(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
	if client.calls != 0 || len(n.messages) != 0 {
		t.Fatal("health-check failure triggered authentication or notification")
	}
}
