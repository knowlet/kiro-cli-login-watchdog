package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
)

type DeviceFlow struct {
	Code string
	URL  string
}

type Client struct {
	bin            string
	authMethod     string
	identityURL    string
	region         string
	commandTimeout time.Duration
	loginTimeout   time.Duration
	logger         *log.Logger
}

func New(cfg config.Config, logger *log.Logger) *Client {
	return &Client{bin: cfg.KiroBin, authMethod: cfg.AuthMethod,
		identityURL: cfg.IdentityProvider, region: cfg.Region,
		commandTimeout: cfg.CommandTimeout, loginTimeout: cfg.LoginTimeout, logger: logger}
}

// command deliberately uses argv, never a shell. bin is trusted local operator
// configuration (KCLW_KIRO_BIN), not CLI output or notification input. Do not
// split it on spaces: an executable path may legitimately contain spaces.
func (c *Client) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	// Bound waiting on descriptors inherited by a misbehaving descendant.
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func (c *Client) WhoAmI(ctx context.Context) (bool, string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, c.commandTimeout)
	defer cancel()
	// Keep the health check compatible with Kiro CLI versions that do not support
	// `whoami --format json`. We only need the exit status plus the explicit
	// logged-out diagnostic; structured output is unnecessary here.
	out, err := c.command(commandCtx, "whoami").CombinedOutput()
	output := strings.TrimSpace(string(out))
	if commandCtx.Err() != nil {
		return false, output, fmt.Errorf("kiro-cli whoami interrupted: %w", commandCtx.Err())
	}
	if err == nil {
		return true, output, nil
	}
	// A non-zero exit is not a dedicated authentication status. Only recognize
	// explicit logged-out diagnostics; flags, service, spawn and signal failures
	// must not start an interactive login. Do not log raw command output here.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 && isLoggedOut(output) {
		return false, output, nil
	}
	return false, output, fmt.Errorf("kiro-cli whoami failed: %w", err)
}

var loggedOutRE = regexp.MustCompile("(?i)^(?:error:\\s*)?(?:you are )?not logged in[.!]?(?:\\s+(?:please (?:log in with|run)|run) `?kiro-cli login`?\\.?)?$")

func isLoggedOut(output string) bool {
	clean := strings.TrimSpace(ansiRE.ReplaceAllString(output, ""))
	if loggedOutRE.MatchString(clean) {
		return true
	}
	var diagnostic struct {
		Error string `json:"error"`
	}
	return json.Unmarshal([]byte(clean), &diagnostic) == nil && loggedOutRE.MatchString(diagnostic.Error)
}

func (c *Client) Logout(ctx context.Context) error {
	commandCtx, cancel := context.WithTimeout(ctx, c.commandTimeout)
	defer cancel()
	_, err := c.command(commandCtx, "logout").CombinedOutput()
	if commandCtx.Err() != nil {
		return fmt.Errorf("kiro-cli logout interrupted: %w", commandCtx.Err())
	}
	if err != nil {
		return fmt.Errorf("kiro-cli logout: %w", err)
	}
	return nil
}

func (c *Client) Login(ctx context.Context, onDeviceFlow func(DeviceFlow) error) error {
	loginCtx, cancel := context.WithTimeout(ctx, c.loginTimeout)
	defer cancel()
	parser := NewDeviceFlowParser(func(flow DeviceFlow) error {
		if onDeviceFlow != nil {
			if err := onDeviceFlow(flow); err != nil {
				cancel()
				return err
			}
		}
		return nil
	})
	stdout := &loginOutput{client: c, parser: parser, cancel: cancel}
	stderr := &loginOutput{client: c, parser: parser, cancel: cancel}
	cmd := c.command(loginCtx, c.LoginArgs()...)
	// With Writer outputs, os/exec owns the reader goroutines and Wait drains
	// both streams before returning. No StdoutPipe/Wait close-before-read race.
	cmd.Stdout, cmd.Stderr = stdout, stderr
	waitErr := cmd.Run()
	stdout.flush()
	stderr.flush()
	if err := parser.Err(); err != nil {
		return err
	}
	if stdout.err != nil {
		return stdout.err
	}
	if stderr.err != nil {
		return stderr.err
	}
	if loginCtx.Err() != nil {
		return fmt.Errorf("kiro-cli login interrupted: %w", loginCtx.Err())
	}
	if waitErr != nil {
		return fmt.Errorf("kiro-cli login failed: %w", waitErr)
	}
	return nil
}

func (c *Client) LoginArgs() []string {
	args := []string{"login"}
	switch c.authMethod {
	case config.AuthIdentityCenter:
		args = append(args, "--license", "pro", "--identity-provider", c.identityURL, "--region", c.region)
	case config.AuthGoogle:
		args = append(args, "--social", "google")
	case config.AuthGitHub:
		args = append(args, "--social", "github")
	case config.AuthBuilderID:
		args = append(args, "--license", "free")
	}
	return append(args, "--use-device-flow")
}

const maxLoginLine = 1024 * 1024

// Each stream has one Writer; os/exec joins both copying goroutines before
// Login inspects err or flushes a final line without a newline.
type loginOutput struct {
	client  *Client
	parser  *DeviceFlowParser
	cancel  context.CancelFunc
	pending []byte
	err     error
}

func (w *loginOutput) Write(data []byte) (int, error) {
	total := len(data)
	for len(data) > 0 {
		n := bytes.IndexByte(data, '\n')
		complete := n >= 0
		if !complete {
			n = len(data)
		}
		if len(w.pending)+n > maxLoginLine {
			w.err = errors.New("kiro-cli login output line exceeds 1 MiB")
			w.pending = nil
			w.cancel()
			return total - len(data), w.err
		}
		w.pending = append(w.pending, data[:n]...)
		data = data[n:]
		if complete {
			w.flush()
			data = data[1:]
		}
	}
	return total, nil
}

func (w *loginOutput) flush() {
	if len(w.pending) == 0 {
		return
	}
	line := string(w.pending)
	w.pending = w.pending[:0]
	if w.client.logger != nil {
		w.client.logger.Printf("kiro-cli: %s", sanitizeLogLine(line))
	}
	w.parser.Feed(line)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func sanitizeLogLine(line string) string {
	clean := ansiRE.ReplaceAllString(line, "")
	if codeRE.MatchString(clean) {
		return "[device code emitted; redacted]"
	}
	if len(deviceURLs(clean)) > 0 {
		return "[device login URL emitted; redacted]"
	}
	return clean
}
