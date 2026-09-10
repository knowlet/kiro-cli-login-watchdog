package watchdog

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/kiro"
	"github.com/knowlet/kiro-cli-login-watchdog/internal/notifier"
)

type KiroClient interface {
	WhoAmI(context.Context) (bool, string, error)
	Logout(context.Context) error
	Login(context.Context, func(kiro.DeviceFlow) error) error
}

type Watchdog struct {
	cfg         config.Config
	kiro        KiroClient
	notifier    notifier.Notifier
	logger      *log.Logger
	lastRelogin time.Time
}

func New(cfg config.Config, client KiroClient, n notifier.Notifier, logger *log.Logger) *Watchdog {
	w := &Watchdog{cfg: cfg, kiro: client, notifier: n, logger: logger}
	if cfg.ForceReloginInterval > 0 {
		w.lastRelogin = time.Now()
	}
	return w
}

func (w *Watchdog) Run(ctx context.Context) error {
	w.logger.Printf("watchdog started; check_interval=%s force_relogin_interval=%s auth_method=%s", w.cfg.CheckInterval, w.cfg.ForceReloginInterval, w.cfg.AuthMethod)
	if err := w.Check(ctx); err != nil {
		w.logger.Printf("initial check failed: %v", err)
	}

	ticker := time.NewTicker(w.cfg.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Printf("watchdog stopping: %v", ctx.Err())
			return nil
		case <-ticker.C:
			if err := w.Check(ctx); err != nil {
				w.logger.Printf("check failed: %v", err)
			}
		}
	}
}

func (w *Watchdog) Check(ctx context.Context) error {
	loggedIn, _, err := w.kiro.WhoAmI(ctx)
	if err != nil {
		return err
	}

	force := loggedIn && w.cfg.ForceReloginInterval > 0 && !w.lastRelogin.IsZero() && time.Since(w.lastRelogin) >= w.cfg.ForceReloginInterval
	if loggedIn && !force {
		w.logger.Printf("kiro-cli authentication is healthy")
		return nil
	}

	if force {
		w.logger.Printf("proactive re-login interval reached; logging out")
		if err := w.kiro.Logout(ctx); err != nil {
			return err
		}
	} else {
		w.logger.Printf("kiro-cli is not authenticated; starting device-flow login")
	}

	notified := false
	err = w.kiro.Login(ctx, func(flow kiro.DeviceFlow) error {
		notified = true
		message := fmt.Sprintf(
			"Kiro CLI login required\n\nCode: %s\nOpen: %s\n\nComplete the sign-in in your browser. The watchdog will keep waiting for Kiro CLI to finish the device flow.",
			flow.Code, flow.URL,
		)
		notifyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return w.notifier.Notify(notifyCtx, message)
	})
	if err != nil {
		if !notified {
			w.logger.Printf("login failed before a device code could be delivered")
		}
		return err
	}

	loggedIn, _, verifyErr := w.kiro.WhoAmI(ctx)
	if verifyErr != nil {
		return fmt.Errorf("verify login: %w", verifyErr)
	}
	if !loggedIn {
		return fmt.Errorf("kiro-cli login command returned successfully but whoami is still unauthenticated")
	}
	w.lastRelogin = time.Now()
	w.logger.Printf("kiro-cli login restored")

	if w.cfg.NotifySuccess {
		notifyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := w.notifier.Notify(notifyCtx, "Kiro CLI login restored successfully."); err != nil {
			return fmt.Errorf("send success notification: %w", err)
		}
	}
	return nil
}

func RedactOutput(output string) string {
	// Reserved for future diagnostic notifications. Do not let credentials or
	// device URLs leak into logs/messages by default.
	if strings.TrimSpace(output) == "" {
		return ""
	}
	return "[kiro-cli output redacted]"
}
