package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	AuthIdentityCenter = "identity-center"
	AuthBuilderID      = "builder-id"
	AuthGoogle         = "google"
	AuthGitHub         = "github"
)

type Config struct {
	KiroBin              string
	AuthMethod           string
	IdentityProvider     string
	Region               string
	CheckInterval        time.Duration
	ForceReloginInterval time.Duration
	CommandTimeout       time.Duration
	LoginTimeout         time.Duration
	TelegramBotToken     string
	TelegramChatID       string
	NotifySuccess        bool
	StateDir             string
	PIDFile              string
	LogFile              string
}

func Load() (Config, error) {
	stateDir, err := defaultStateDir()
	if err != nil {
		return Config{}, fmt.Errorf("resolve state directory: %w", err)
	}
	if v := strings.TrimSpace(os.Getenv("KCLW_STATE_DIR")); v != "" {
		stateDir = expandHome(v)
	}

	cfg := Config{
		KiroBin:              envOr("KCLW_KIRO_BIN", "kiro-cli"),
		AuthMethod:           strings.ToLower(envOr("KCLW_AUTH_METHOD", AuthIdentityCenter)),
		IdentityProvider:     strings.TrimSpace(os.Getenv("KCLW_IDENTITY_PROVIDER")),
		Region:               strings.TrimSpace(os.Getenv("KCLW_REGION")),
		CheckInterval:        5 * time.Minute,
		ForceReloginInterval: 0,
		CommandTimeout:       20 * time.Second,
		LoginTimeout:         15 * time.Minute,
		TelegramBotToken:     strings.TrimSpace(os.Getenv("KCLW_TELEGRAM_BOT_TOKEN")),
		TelegramChatID:       strings.TrimSpace(os.Getenv("KCLW_TELEGRAM_CHAT_ID")),
		NotifySuccess:        true,
		StateDir:             stateDir,
		PIDFile:              filepath.Join(stateDir, "watchdog.pid"),
		LogFile:              filepath.Join(stateDir, "watchdog.log"),
	}

	if cfg.CheckInterval, err = durationEnv("KCLW_CHECK_INTERVAL", cfg.CheckInterval); err != nil {
		return Config{}, err
	}
	if cfg.ForceReloginInterval, err = durationEnv("KCLW_FORCE_RELOGIN_INTERVAL", cfg.ForceReloginInterval); err != nil {
		return Config{}, err
	}
	if cfg.CommandTimeout, err = durationEnv("KCLW_COMMAND_TIMEOUT", cfg.CommandTimeout); err != nil {
		return Config{}, err
	}
	if cfg.LoginTimeout, err = durationEnv("KCLW_LOGIN_TIMEOUT", cfg.LoginTimeout); err != nil {
		return Config{}, err
	}
	if cfg.NotifySuccess, err = boolEnv("KCLW_NOTIFY_SUCCESS", cfg.NotifySuccess); err != nil {
		return Config{}, err
	}
	if v := strings.TrimSpace(os.Getenv("KCLW_PID_FILE")); v != "" {
		cfg.PIDFile = expandHome(v)
	}
	if v := strings.TrimSpace(os.Getenv("KCLW_LOG_FILE")); v != "" {
		cfg.LogFile = expandHome(v)
	}

	return cfg, nil
}

func (c Config) ValidateRuntime() error {
	if c.KiroBin == "" {
		return errors.New("KCLW_KIRO_BIN cannot be empty")
	}
	switch c.AuthMethod {
	case AuthIdentityCenter:
		if c.IdentityProvider == "" {
			return errors.New("KCLW_IDENTITY_PROVIDER is required for identity-center auth")
		}
		if c.Region == "" {
			return errors.New("KCLW_REGION is required for identity-center auth")
		}
	case AuthBuilderID, AuthGoogle, AuthGitHub:
		// Supported by Kiro's device flow. No extra fields are required.
	default:
		return fmt.Errorf("unsupported KCLW_AUTH_METHOD %q", c.AuthMethod)
	}
	if c.CheckInterval <= 0 {
		return errors.New("KCLW_CHECK_INTERVAL must be > 0")
	}
	if c.ForceReloginInterval < 0 {
		return errors.New("KCLW_FORCE_RELOGIN_INTERVAL cannot be negative")
	}
	if c.CommandTimeout <= 0 || c.LoginTimeout <= 0 {
		return errors.New("KCLW_COMMAND_TIMEOUT and KCLW_LOGIN_TIMEOUT must be > 0")
	}
	if (c.TelegramBotToken == "") != (c.TelegramChatID == "") {
		return errors.New("KCLW_TELEGRAM_BOT_TOKEN and KCLW_TELEGRAM_CHAT_ID must be set together")
	}
	if c.TelegramBotToken == "" {
		return errors.New("telegram notifier is required in phase 1: set KCLW_TELEGRAM_BOT_TOKEN and KCLW_TELEGRAM_CHAT_ID")
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func defaultStateDir() (string, error) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "kiro-cli-login-watchdog"), nil
		}
	}
	if stateHome := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); stateHome != "" {
		return filepath.Join(stateHome, "kiro-cli-login-watchdog"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "kiro-cli-login-watchdog"), nil
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
