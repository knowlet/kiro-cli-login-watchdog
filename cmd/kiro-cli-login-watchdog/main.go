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
		fmt.Fprintln(os.Stderr, "error:", err)
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
		}
		switch command {
		case "run":
			return runForeground(cfg, false)
		case "once":
			return runForeground(cfg, true)
		case "start":
			return startDaemon(cfg, *envFile)
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
	defer daemon.RemovePID(cfg.PIDFile, os.Getpid())
	logger := log.New(os.Stderr, "kclw ", log.LstdFlags|log.Lmsgprefix)
	client := kiro.New(cfg, logger)
	n := notifier.NewTelegram(cfg.TelegramBotToken, cfg.TelegramChatID)
	w := watchdog.New(cfg, client, n, logger)

	ctx, cancel := signalContext()
	defer cancel()
	if once {
		return w.Check(ctx)
	}
	return w.Run(ctx)
}

func startDaemon(cfg config.Config, envFile string) error {
	if pid, err := daemon.ReadPID(cfg.PIDFile); err == nil {
		if daemon.ProcessAlive(pid) {
			return fmt.Errorf("watchdog is already running (pid %d)", pid)
		}
		_ = os.Remove(cfg.PIDFile)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	logFile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	childArgs := []string{"run"}
	if envFile != "" {
		// The env file has already been loaded and the child inherits the
		// resulting environment. Keep the original path only for transparent
		// behavior if the user inspects the child command line.
		childArgs = append(childArgs, "--env-file", envFile)
	}
	cmd := exec.Command(exe, childArgs...)
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	daemon.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	if err := daemon.WritePID(cfg.PIDFile, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("write pid file: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release daemon process: %w", err)
	}

	fmt.Printf("started kiro-cli-login-watchdog (pid %d)\nlog: %s\n", mustReadPID(cfg.PIDFile), cfg.LogFile)
	return nil
}

func stopDaemon(cfg config.Config) error {
	pid, err := daemon.ReadPID(cfg.PIDFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("watchdog is not running (pid file not found)")
		}
		return err
	}
	if !daemon.ProcessAlive(pid) {
		_ = os.Remove(cfg.PIDFile)
		return fmt.Errorf("watchdog is not running (removed stale pid file for pid %d)", pid)
	}
	if err := daemon.Stop(pid); err != nil {
		return fmt.Errorf("stop pid %d: %w", pid, err)
	}
	if !daemon.WaitForExit(pid, 5*time.Second) {
		return fmt.Errorf("pid %d did not exit after stop request", pid)
	}
	_ = os.Remove(cfg.PIDFile)
	fmt.Printf("stopped kiro-cli-login-watchdog (pid %d)\n", pid)
	return nil
}

func statusDaemon(cfg config.Config) error {
	pid, err := daemon.ReadPID(cfg.PIDFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("stopped")
			return nil
		}
		return err
	}
	if !daemon.ProcessAlive(pid) {
		_ = os.Remove(cfg.PIDFile)
		fmt.Printf("stopped (removed stale pid file for pid %d)\n", pid)
		return nil
	}
	fmt.Printf("running (pid %d)\nlog: %s\n", pid, cfg.LogFile)
	return nil
}

func signalContext() (context.Context, context.CancelFunc) {
	if runtime.GOOS == "windows" {
		return signal.NotifyContext(context.Background(), os.Interrupt)
	}
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func mustReadPID(path string) int {
	pid, _ := daemon.ReadPID(path)
	return pid
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
