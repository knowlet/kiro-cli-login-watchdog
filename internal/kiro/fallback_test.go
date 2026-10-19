package kiro

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginWeakFallbackBeforeChildExits(t *testing.T) {
	var logs bytes.Buffer
	c := helperClient(t, "weak-wait", log.New(&logs, "", 0))
	c.loginTimeout = 2 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	var got DeviceFlow
	err := c.Login(ctx, func(flow DeviceFlow) error {
		calls++
		got = flow
		// The fake child can only stop after notification or its deadline.
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("notification did not unblock the waiting child before timeout: %v", err)
	}
	if calls != 1 || got.Code != "ABCD-EFGH" || got.URL != "https://example.test/device" {
		t.Fatalf("flow=%+v calls=%d", got, calls)
	}
	if strings.Contains(logs.String(), "ABCD-EFGH") || strings.Contains(logs.String(), "https://") {
		t.Fatal("credentials leaked into logs")
	}
}

func TestLoginStrongerURLWinsDuringFallbackGrace(t *testing.T) {
	c := helperClient(t, "weak-then-strong", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	var got DeviceFlow
	err := c.Login(ctx, func(flow DeviceFlow) error {
		calls++
		got = flow
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if calls != 1 || got.Code != "IJKL-MNOP" || got.URL != "https://example.test/activate?user_code=IJKL-MNOP" {
		t.Fatalf("flow=%+v calls=%d", got, calls)
	}
}

func TestLoginWeakFallbackDoesNotNotifyAfterInterruption(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c := helperClient(t, "weak-wait", nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantErr := context.DeadlineExceeded
			if mode == "deadline" {
				c.loginTimeout = 100 * time.Millisecond
			} else {
				wantErr = context.Canceled
				timer := time.AfterFunc(100*time.Millisecond, cancel)
				defer timer.Stop()
			}
			var calls atomic.Int32
			err := c.Login(ctx, func(DeviceFlow) error { calls.Add(1); return nil })
			if !errors.Is(err, wantErr) {
				t.Fatalf("err=%v", err)
			}
			// Also observe past the grace interval: no callback may outlive Login.
			time.Sleep(600 * time.Millisecond)
			if calls.Load() != 0 {
				t.Fatalf("interrupted login delivered %d stale prompts", calls.Load())
			}
		})
	}
}

func TestLoginWeakFallbackCallbackFailureCancelsChild(t *testing.T) {
	c := helperClient(t, "weak-wait", nil)
	c.loginTimeout = 2 * time.Second
	sentinel := errors.New("notifier unavailable")
	started := time.Now()
	err := c.Login(context.Background(), func(DeviceFlow) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(started) >= c.loginTimeout {
		t.Fatal("callback failure was only delivered after the login deadline")
	}
}

func TestFallbackWaiterJoinsActiveCallback(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	p := NewDeviceFlowParser(func(DeviceFlow) error {
		close(entered)
		<-release
		return nil
	})
	stop := startDeviceFlowFallback(context.Background(), p)
	defer func() {
		releaseOnce.Do(func() { close(release) })
		stop()
	}()
	p.Feed("Code: ABCD-EFGH; Visit https://example.test/device")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fallback did not start")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("waiter returned while the callback was still running")
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("waiter did not join the completed callback")
	}
}

func TestWeakFallbackConcurrentDeliveryExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	delivered := make(chan DeviceFlow, 32)
	p := NewDeviceFlowParser(func(flow DeviceFlow) error {
		calls.Add(1)
		delivered <- flow
		return nil
	})
	stop := startDeviceFlowFallback(context.Background(), p)
	defer stop()
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Feed("Code: ABCD-EFGH")
			p.Feed("Visit https://example.test/device")
		}()
	}
	wg.Wait()
	select {
	case flow := <-delivered:
		if flow.Code != "ABCD-EFGH" || flow.URL != "https://example.test/device" {
			t.Fatalf("flow=%+v", flow)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fallback was not delivered")
	}
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Feed("Open this URL: https://example.test/activate?user_code=IJKL-MNOP")
			p.Finalize()
		}()
	}
	wg.Wait()
	stop()
	if calls.Load() != 1 {
		t.Fatalf("callbacks=%d", calls.Load())
	}
}
