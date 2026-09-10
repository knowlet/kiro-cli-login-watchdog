package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/daemon"
)

func TestMain(m *testing.M) {
	if os.Getenv("KCLW_MAIN_HELPER") == "1" && len(os.Args) > 1 {
		switch os.Args[1] {
		case "whoami":
			if os.Getenv("KCLW_MAIN_CASE") == "login-hang" {
				fmt.Fprintln(os.Stderr, "Not logged in")
				os.Exit(1)
			}
			fmt.Println(`{"authenticated":true}`)
			os.Exit(0)
		case "login":
			// A listener proves the actual login child exited, rather than relying on
			// PID liveness or on a heartbeat whose timing could make a flaky test.
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				os.Exit(2)
			}
			if os.WriteFile(os.Getenv("KCLW_CHILD_MARKER"), []byte(listener.Addr().String()), 0o600) != nil {
				os.Exit(2)
			}
			defer listener.Close()
			for {
				time.Sleep(time.Second)
			}
		default:
			if err := run(os.Args[1:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func helperEnvironment(t *testing.T, mode string) (string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for key, value := range map[string]string{
		"KCLW_MAIN_HELPER": "1", "KCLW_MAIN_CASE": mode,
		"KCLW_KIRO_BIN": exe, "KCLW_AUTH_METHOD": "builder-id", "KCLW_STATE_DIR": dir,
		"KCLW_PID_FILE": filepath.Join(dir, "watchdog.pid"), "KCLW_LOG_FILE": filepath.Join(dir, "watchdog.log"),
		"KCLW_TELEGRAM_BOT_TOKEN": "test-placeholder", "KCLW_TELEGRAM_CHAT_ID": "0",
		"KCLW_CHECK_INTERVAL": "1h", "KCLW_COMMAND_TIMEOUT": "10s", "KCLW_LOGIN_TIMEOUT": "1m",
		"KCLW_FORCE_RELOGIN_INTERVAL": "0", "KCLW_CHILD_MARKER": filepath.Join(dir, "child-address"),
	} {
		t.Setenv(key, value)
	}
	return exe, dir
}

func invoke(exe string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
	return string(out), err
}

func requireCommand(t *testing.T, exe, command string) string {
	t.Helper()
	out, err := invoke(exe, command)
	if err != nil {
		t.Fatalf("%s: %v\n%s", command, err, out)
	}
	return out
}

func TestConcurrentStartLifecycle(t *testing.T) {
	exe, _ := helperEnvironment(t, "healthy")
	t.Cleanup(func() { _, _ = invoke(exe, "stop") })
	const workers = 8
	type result struct {
		out string
		err error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); out, err := invoke(exe, "start"); results <- result{out, err} }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for r := range results {
		if r.err == nil {
			successes++
		} else if !strings.Contains(r.out, "already active") {
			t.Errorf("unexpected start failure: %s", r.out)
		}
	}
	if successes != 1 {
		t.Fatalf("successful launches=%d", successes)
	}
	if out := requireCommand(t, exe, "status"); !strings.Contains(out, "running (pid") {
		t.Fatal(out)
	}
	if _, err := invoke(exe, "once"); err == nil {
		t.Fatal("once overlapped the daemon")
	}
	requireCommand(t, exe, "stop")
	if out := requireCommand(t, exe, "status"); strings.TrimSpace(out) != "stopped" {
		t.Fatal(out)
	}
	requireCommand(t, exe, "start")
	requireCommand(t, exe, "stop")
}

func TestStalePIDDoesNotIdentifyWatchdog(t *testing.T) {
	exe, dir := helperEnvironment(t, "healthy")
	// This PID is live, but never identifies a watchdog. Status must reject it
	// before the test proceeds to stop, guarding against the old signal-0 bug.
	if err := os.WriteFile(filepath.Join(dir, "watchdog.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := requireCommand(t, exe, "status"); strings.TrimSpace(out) != "stopped" {
		t.Fatalf("unrelated PID was trusted: %s", out)
	}
	if _, err := invoke(exe, "stop"); err == nil {
		t.Fatal("stale PID was stopped")
	}
	requireCommand(t, exe, "start")
	t.Cleanup(func() { _, _ = invoke(exe, "stop") })
	requireCommand(t, exe, "stop")
}

func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file was not ready: %s", path)
	return nil
}

func TestStopReapsActiveLoginChild(t *testing.T) {
	exe, dir := helperEnvironment(t, "login-hang")
	requireCommand(t, exe, "start")
	t.Cleanup(func() { _, _ = invoke(exe, "stop") })
	address := string(waitForFile(t, filepath.Join(dir, "child-address")))
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("login child not active: %v", err)
	}
	conn.Close()
	requireCommand(t, exe, "stop")
	if conn, err = net.DialTimeout("tcp", address, 200*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("login child survived stop")
	}
}

func TestCrashReleasesLockAndAllowsRestart(t *testing.T) {
	exe, dir := helperEnvironment(t, "healthy")
	child := exec.Command(exe, "run")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	path := filepath.Join(dir, "watchdog.pid")
	waitForFile(t, path)
	record, err := daemon.ReadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	// Waiting for the parent is not a barrier for all references to its lock:
	// a concurrent fork can retain one until exec closes inherited descriptors.
	// Assert bounded lock release, not same-instant release after process wait.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := daemon.WaitStopped(ctx, path, record); err != nil {
		t.Fatalf("crashed instance did not release its lifetime lock: %v", err)
	}
	// The retained Windows process handle/stale state is not proof of life.
	if _, running, err := daemon.Status(context.Background(), path); err != nil || running {
		t.Fatalf("running=%v err=%v", running, err)
	}
	requireCommand(t, exe, "start")
	t.Cleanup(func() { _, _ = invoke(exe, "stop") })
	requireCommand(t, exe, "stop")
}
