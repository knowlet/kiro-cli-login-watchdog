package watchdog

import (
	"context"
	"fmt"
	"log"
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
	cfg                      config.Config
	kiro                     KiroClient
	notifier                 notifier.Notifier
	logger                   *log.Logger
	lastRelogin              time.Time
	loginPendingVerification bool
}

func New(cfg config.Config, client KiroClient, n notifier.Notifier, logger *log.Logger) *Watchdog {
	w := &Watchdog{cfg: cfg, kiro: client, notifier: n, logger: logger}
	if cfg.ForceReloginInterval > 0 {
		w.lastRelogin = time.Now()
	}
	return w
}
func (w *Watchdog) Run(ctx context.Context) error {
	w.logger.Printf("watchdog started; check_interval=%s force_relogin_interval=%s backend=%s auth_method=%s", w.cfg.CheckInterval, w.cfg.ForceReloginInterval, w.cfg.DisplayName(), w.cfg.AuthMethod)
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
	if w.loginPendingVerification {
		if loggedIn {
			return w.loginRestored(ctx)
		}
		w.loginPendingVerification = false
	}
	force := loggedIn && w.cfg.ForceReloginInterval > 0 && !w.lastRelogin.IsZero() && time.Since(w.lastRelogin) >= w.cfg.ForceReloginInterval
	if loggedIn && !force {
		w.logger.Printf("%s authentication is healthy", w.cfg.DisplayName())
		return nil
	}
	if force {
		w.logger.Printf("proactive re-login interval reached; logging out")
		if err := w.kiro.Logout(ctx); err != nil {
			return err
		}
	} else {
		w.logger.Printf("%s is not authenticated; starting device-flow login", w.cfg.DisplayName())
	}
	notified := false
	err = w.kiro.Login(ctx, func(flow kiro.DeviceFlow) error {
		notified = true
		message := fmt.Sprintf("%s login required\n\nCode: %s\nOpen: %s\n\nComplete the sign-in in your browser. The watchdog will keep waiting for %s to finish the device flow.", w.cfg.DisplayName(), flow.Code, flow.URL, w.cfg.DisplayName())
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
		// Retry verification before forcing another destructive logout. A
		// successful login command alone must not reset the re-login interval.
		w.loginPendingVerification = true
		return fmt.Errorf("verify login: %w", verifyErr)
	}
	if !loggedIn {
		return fmt.Errorf("%s login command returned successfully but whoami is still unauthenticated", w.cfg.DisplayName())
	}
	return w.loginRestored(ctx)
}

func (w *Watchdog) loginRestored(ctx context.Context) error {
	w.loginPendingVerification = false
	w.lastRelogin = time.Now()
	w.logger.Printf("%s login restored", w.cfg.DisplayName())
	if w.cfg.NotifySuccess {
		notifyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := w.notifier.Notify(notifyCtx, w.cfg.DisplayName()+" login restored successfully."); err != nil {
			return fmt.Errorf("send success notification: %w", err)
		}
	}
	return nil
}
