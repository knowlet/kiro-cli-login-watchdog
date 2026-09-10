package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLifetimeLockAndAuthenticatedShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watchdog.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	instance, err := Start(path, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if _, err := Start(path, func() {}); !errors.Is(err, ErrLocked) {
		t.Fatalf("second instance: %v", err)
	}
	record, running, err := Status(context.Background(), path)
	if err != nil || !running {
		t.Fatalf("status=%v err=%v", running, err)
	}
	wrong := record
	wrong.Token = strings.Repeat("0", 64)
	if err := Control(context.Background(), wrong, true); err == nil {
		t.Fatal("wrong token was accepted")
	}
	if ctx.Err() != nil {
		t.Fatal("unauthenticated request stopped watchdog")
	}
	if err := Control(context.Background(), record, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("no graceful cancellation")
	}
	// Cancellation alone must not release the lock; command cleanup happens first.
	if lock, err := Acquire(path + ".lock"); err == nil {
		lock.Close()
		t.Fatal("lock released before cleanup")
	}
	instance.Close()
	if _, running, err = Status(context.Background(), path); err != nil || running {
		t.Fatalf("stopped status=%v err=%v", running, err)
	}
	replacement, err := Start(path, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.Record.Token == record.Token {
		t.Fatal("instance token reused")
	}
}

func TestConcurrentInstanceClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watchdog.pid")
	const workers = 16
	results := make(chan *Instance, workers)
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			instance, err := Start(path, func() {})
			if err != nil {
				failures <- err
				return
			}
			results <- instance
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	successes := 0
	for instance := range results {
		successes++
		instance.Close()
	}
	for err := range failures {
		if !errors.Is(err, ErrLocked) {
			t.Error(err)
		}
	}
	if successes != 1 {
		t.Fatalf("live instances=%d", successes)
	}
}

func TestStaleStateNeverContactsUnrelatedProcess(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "watchdog.pid")
	record := Record{PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: strings.Repeat("a", 64)}
	if err := writeRecord(path, record); err != nil {
		t.Fatal(err)
	}
	if _, running, err := Status(context.Background(), path); err != nil || running {
		t.Fatalf("stale status=%v err=%v", running, err)
	}
	if calls.Load() != 0 {
		t.Fatal("stale address was contacted")
	}
	if err := os.WriteFile(path, []byte("1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecord(path); err == nil {
		t.Fatal("legacy PID trusted")
	}
	instance, err := Start(path, func() {})
	if err != nil {
		t.Fatal(err)
	}
	instance.Close()
}

func TestControlRejectsRemoteAddressAndRedirect(t *testing.T) {
	record := Record{PID: 1, Address: "example.com:80", Token: strings.Repeat("a", 64)}
	if err := Control(context.Background(), record, true); err == nil {
		t.Fatal("non-loopback target allowed")
	}
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	record.Address = strings.TrimPrefix(redirect.URL, "http://")
	if err := Control(context.Background(), record, false); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked.Load() != 0 {
		t.Fatal("control credential redirected")
	}
}

func TestLockedButUnverifiableStateFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watchdog.pid")
	lock, err := Acquire(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, running, err := Status(context.Background(), path); err == nil || running {
		t.Fatalf("status=%v err=%v", running, err)
	}
}
