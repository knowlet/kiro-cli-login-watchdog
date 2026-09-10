package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitStoppedRequiresLockRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watchdog.pid")
	instance, err := Start(path, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := WaitStopped(ctx, path, instance.Record); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held lifetime lock should time out, got %v", err)
	}
	instance.Close()
	ctx, cancelReleased := context.WithTimeout(context.Background(), time.Second)
	defer cancelReleased()
	if err := WaitStopped(ctx, path, instance.Record); err != nil {
		t.Fatalf("released lifetime lock was not observed: %v", err)
	}
}
