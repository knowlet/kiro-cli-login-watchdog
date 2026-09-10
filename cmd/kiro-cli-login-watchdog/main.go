package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/daemon"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/kiro"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/notifier"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/watchdog"
)

const version = "0.1.0-dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	command := args[0]
	switch command {
	case "run", "once", "start", "stop", "status":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		envFile := fs.String("env-file", "", "load configuration from KEY=VALUE file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return usageError()
		}
		if err := config.LoadEnvFile(*envFile); err != nil {
			return err
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if command == "run" || command == "once" || command == "start" {
			if err := cfg.ValidateRuntime(); err != nil {
				return err
			}
			resolved, err := exec.LookPath(cfg.KiroBin)
			if err != nil {
				return fmt.Errorf("resolve KCLW_KIRO_BIN: %w", err)
			}
			cfg.KiroBin, err = filepath.Abs(resolved)
			if err != nil {
				return fmt.Errorf("resolve executable path: %w", err)
			}
		}
		switch command {
		case "run":
			return runForeground(cfg, false)
		case "once":
			return runForeground(cfg, true)
		case "start":
			return startDaemon(cfg)
		case "stop":
			return stopDaemon(cfg)
		case "status":
			return statusDaemon(cfg)
		}
	case "version", "--version", "-v":
		fmt.Printf("kiro-cli-login-watchdog %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return usageError()
	}
	return nil
}

func runForeground(cfg config.Config, once bool) error {
	ctx, cancel := signalContext()
	defer cancel()
	instance, err := daemon.Start(cfg.PIDFile, cancel)
	if err != nil {
		return fmt.Errorf("claim watchdog instance: %w", err)
	}
	defer instance.Close()
	logger := log.New(os.Stderr, "kclw ", log.LstdFlags|log.Lmsgprefix)
	client := kiro.New(cfg, logger)
	n := notifier.NewTelegram(cfg.TelegramBotToken, cfg.TelegramChatID)
	w := watchdog.New(cfg, client, n, logger)
	if once {
		return w.Check(ctx)
	}
	return w.Run(ctx)
}

func startDaemon(cfg config.Config) error {
	// Serialize launch/stop transitions. The child independently acquires and
	// holds the instance lock for its entire lifetime, including foreground mode.
	transition, err := daemon.Acquire(cfg.PIDFile + ".control.lock")
	if err != nil {
		return err
	}
	defer func() { _ = transition.Close() }()
	probe, err := daemon.Acquire(cfg.PIDFile + ".lock")
	if err != nil {
		return err
	}
	if err := probe.Close(); err != nil {
		return fmt.Errorf("release instance probe: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	logFile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	// Only re-exec this binary; no shell and no user-supplied executable/argv.
	// The env file was already loaded. Inherit it rather than loading it twice.
	cmd := exec.Command(exe, "run")
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	daemon.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, readErr := daemon.ReadRecord(cfg.PIDFile)
		if readErr == nil && record.PID == pid && daemon.Control(context.Background(), record, false) == nil {
			if err := cmd.Process.Release(); err != nil {
				return fmt.Errorf("release daemon process: %w", err)
			}
			fmt.Printf("started kiro-cli-login-watchdog (pid %d)\nlog: %s\n", pid, cfg.LogFile)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// This is our retained child handle, never a PID loaded from a file.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return fmt.Errorf("daemon did not become ready; inspect %s", cfg.LogFile)
}

func stopDaemon(cfg config.Config) error {
	transition, err := daemon.Acquire(cfg.PIDFile + ".control.lock")
	if err != nil {
		return err
	}
	defer func() { _ = transition.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record, running, err := daemon.Status(ctx, cfg.PIDFile)
	if err != nil {
		return err
	}
	if !running {
		return errors.New("watchdog is not running")
	}
	// Authenticated graceful cancellation on every OS, including Windows.
	// CommandContext terminates and reaps the active kiro-cli before unlocking.
	if err := daemon.Control(ctx, record, true); err != nil {
		return err
	}
	if err := daemon.WaitStopped(ctx, cfg.PIDFile, record); err != nil {
		return fmt.Errorf("wait for watchdog shutdown: %w", err)
	}
	fmt.Printf("stopped kiro-cli-login-watchdog (pid %d)\n", record.PID)
	return nil
}

func statusDaemon(cfg config.Config) error {
	record, running, err := daemon.Status(context.Background(), cfg.PIDFile)
	if err != nil {
		return err
	}
	if !running {
		fmt.Println("stopped")
		return nil
	}
	fmt.Printf("running (pid %d)\nlog: %s\n", record.PID, cfg.LogFile)
	return nil
}

func signalContext() (context.Context, context.CancelFunc) {
	if runtime.GOOS == "windows" {
		return signal.NotifyContext(context.Background(), os.Interrupt)
	}
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func usageError() error {
	printUsage()
	return fmt.Errorf("usage: kiro-cli-login-watchdog <start|stop|status|run|once|version> [--env-file path]")
}

func printUsage() {
	fmt.Print(`kiro-cli-login-watchdog - keep Kiro CLI authentication alive

Usage:
  kiro-cli-login-watchdog start [--env-file .env]   Start detached daemon
  kiro-cli-login-watchdog stop  [--env-file .env]   Stop daemon
  kiro-cli-login-watchdog status [--env-file .env]  Show daemon status
  kiro-cli-login-watchdog run   [--env-file .env]   Run in foreground
  kiro-cli-login-watchdog once  [--env-file .env]   Check once and exit
  kiro-cli-login-watchdog version
`)
}
