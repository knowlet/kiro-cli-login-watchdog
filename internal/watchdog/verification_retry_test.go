package watchdog

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/kiro"
)

type healthResult struct {
	loggedIn bool
	err      error
}

type verificationRetryKiro struct {
	fakeKiro
	health      []healthResult
	logoutCalls int
	logoutErr   error
	loginErr    error
}

func (f *verificationRetryKiro) WhoAmI(ctx context.Context) (bool, string, error) {
	if len(f.health) == 0 {
		return f.fakeKiro.WhoAmI(ctx)
	}
	result := f.health[0]
	f.health = f.health[1:]
	return result.loggedIn, "", result.err
}

func (f *verificationRetryKiro) Logout(ctx context.Context) error {
	f.logoutCalls++
	if f.logoutErr != nil {
		return f.logoutErr
	}
	return f.fakeKiro.Logout(ctx)
}

func (f *verificationRetryKiro) Login(ctx context.Context, cb func(kiro.DeviceFlow) error) error {
	if f.loginErr != nil {
		f.loginCalls++
		return f.loginErr
	}
	return f.fakeKiro.Login(ctx, cb)
}

func TestForcedReloginRetriesVerificationBeforeAnotherLogout(t *testing.T) {
	for _, backend := range []string{config.BackendKiroCLI, config.BackendKAS} {
		t.Run(backend, func(t *testing.T) {
			transient := errors.New("service unavailable")
			client := &verificationRetryKiro{
				fakeKiro: fakeKiro{loggedIn: true},
				health: []healthResult{
					{loggedIn: true}, // The expired interval triggers one forced login.
					{err: transient}, // Immediate verification fails operationally.
					{err: transient}, // The next scheduled check still cannot verify.
					{loggedIn: true}, // The newly signed-in session is finally verified.
				},
			}
			n := &fakeNotifier{}
			w := New(config.Config{Backend: backend, ForceReloginInterval: time.Hour, NotifySuccess: true}, client, n, log.New(io.Discard, "", 0))
			expired := time.Now().Add(-2 * time.Hour)
			w.lastRelogin = expired

			for i := 0; i < 2; i++ {
				if err := w.Check(context.Background()); !errors.Is(err, transient) {
					t.Fatalf("unverified check %d: error = %v", i, err)
				}
				if !w.lastRelogin.Equal(expired) || len(n.messages) != 1 {
					t.Fatal("unverified login reset the interval or reported success")
				}
				if client.logoutCalls != 1 || client.loginCalls != 1 {
					t.Fatalf("unverified check %d repeated authentication: logouts=%d logins=%d", i, client.logoutCalls, client.loginCalls)
				}
			}
			beforeVerification := time.Now()
			if err := w.Check(context.Background()); err != nil {
				t.Fatalf("verification retry: %v", err)
			}
			if client.logoutCalls != 1 || client.loginCalls != 1 {
				t.Fatalf("healthy verification repeated authentication: logouts=%d logins=%d, want 1 each", client.logoutCalls, client.loginCalls)
			}
			if w.lastRelogin.Before(beforeVerification) {
				t.Fatal("verified login did not reset the interval")
			}
			if len(n.messages) != 2 || n.messages[1] != w.cfg.DisplayName()+" login restored successfully." {
				t.Fatalf("notifications after verified recovery = %v", n.messages)
			}
			if err := w.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			if client.loginCalls != 1 || client.logoutCalls != 1 || len(n.messages) != 2 {
				t.Fatal("healthy check repeated login or success notification")
			}

			// A verified retry must not disable later scheduled re-logins.
			w.lastRelogin = expired
			if err := w.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			if client.logoutCalls != 2 || client.loginCalls != 2 {
				t.Fatalf("next expired interval: logouts=%d logins=%d, want 2 each", client.logoutCalls, client.loginCalls)
			}
		})
	}
}

func TestVerificationRetryStillRepairsUnauthenticatedSession(t *testing.T) {
	transient := errors.New("service unavailable")
	client := &verificationRetryKiro{
		fakeKiro: fakeKiro{loggedIn: true},
		health: []healthResult{
			{loggedIn: true}, {err: transient},
			{}, {}, // Retry confirms signed out, and the new login also fails verification.
		},
	}
	n := &fakeNotifier{}
	w := New(config.Config{Backend: config.BackendKAS, ForceReloginInterval: time.Hour, NotifySuccess: true}, client, n, log.New(io.Discard, "", 0))
	expired := time.Now().Add(-2 * time.Hour)
	w.lastRelogin = expired
	if err := w.Check(context.Background()); !errors.Is(err, transient) {
		t.Fatalf("initial verification: %v", err)
	}
	if err := w.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "still unauthenticated") {
		t.Fatalf("unauthenticated retry: %v", err)
	}
	if client.logoutCalls != 1 || client.loginCalls != 2 {
		t.Fatalf("signed-out retry: logouts=%d logins=%d, want 1 and 2", client.logoutCalls, client.loginCalls)
	}
	if !w.lastRelogin.Equal(expired) || len(n.messages) != 2 {
		t.Fatal("failed verification reset the interval or reported success")
	}

	// The explicit signed-out result must discard pending verification, even
	// if some other actor restores the session before the next check.
	if err := w.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.logoutCalls != 2 || client.loginCalls != 3 {
		t.Fatalf("expired interval after failed verification: logouts=%d logins=%d", client.logoutCalls, client.loginCalls)
	}
}

func TestFailedForcedReloginDoesNotResetInterval(t *testing.T) {
	sentinel := errors.New("authentication command failed")
	for _, failure := range []string{"logout", "login", "verification"} {
		t.Run(failure, func(t *testing.T) {
			client := &verificationRetryKiro{fakeKiro: fakeKiro{loggedIn: true}}
			switch failure {
			case "logout":
				client.logoutErr = sentinel
			case "login":
				client.loginErr = sentinel
			case "verification":
				client.health = []healthResult{{loggedIn: true}, {}}
			}
			n := &fakeNotifier{}
			w := New(config.Config{Backend: config.BackendKAS, ForceReloginInterval: time.Hour, NotifySuccess: true}, client, n, log.New(io.Discard, "", 0))
			expired := time.Now().Add(-2 * time.Hour)
			w.lastRelogin = expired
			if err := w.Check(context.Background()); err == nil {
				t.Fatal("failed authentication was treated as success")
			}
			if !w.lastRelogin.Equal(expired) {
				t.Fatal("failed authentication reset the interval")
			}
			for _, msg := range n.messages {
				if strings.Contains(msg, "restored successfully") {
					t.Fatal("failed authentication reported success")
				}
			}
			// A failed command must not leave a successful-login retry marker.
			client.logoutErr, client.loginErr = nil, nil
			client.loggedIn = true
			if err := w.Check(context.Background()); err != nil {
				t.Fatal(err)
			}
			if client.logoutCalls != 2 {
				t.Fatalf("expired interval after failed authentication: logouts=%d, want 2", client.logoutCalls)
			}
		})
	}
}
