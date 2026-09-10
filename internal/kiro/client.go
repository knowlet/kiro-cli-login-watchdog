package kiro

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
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
	return &Client{
		bin:            cfg.KiroBin,
		authMethod:     cfg.AuthMethod,
		identityURL:    cfg.IdentityProvider,
		region:         cfg.Region,
		commandTimeout: cfg.CommandTimeout,
		loginTimeout:   cfg.LoginTimeout,
		logger:         logger,
	}
}

func (c *Client) WhoAmI(ctx context.Context) (bool, string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, c.commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, c.bin, "whoami", "--format", "json")
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if commandCtx.Err() != nil {
		return false, output, fmt.Errorf("kiro-cli whoami timed out: %w", commandCtx.Err())
	}
	if err == nil {
		return true, output, nil
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return false, output, fmt.Errorf("execute %s: %w", c.bin, err)
	}
	// Kiro documents a non-zero "not logged in" error for whoami. Treat other
	// normal command exit statuses as unauthenticated and let Login verify it.
	return false, output, nil
}

func (c *Client) Logout(ctx context.Context) error {
	commandCtx, cancel := context.WithTimeout(ctx, c.commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, c.bin, "logout")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kiro-cli logout: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *Client) Login(ctx context.Context, onDeviceFlow func(DeviceFlow) error) error {
	loginCtx, cancel := context.WithTimeout(ctx, c.loginTimeout)
	defer cancel()

	args := c.LoginArgs()
	cmd := exec.CommandContext(loginCtx, c.bin, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start kiro-cli login: %w", err)
	}

	parser := NewDeviceFlowParser(func(flow DeviceFlow) error {
		if onDeviceFlow == nil {
			return nil
		}
		if err := onDeviceFlow(flow); err != nil {
			cancel()
			return err
		}
		return nil
	})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		c.scanLoginOutput(stdout, parser)
	}()
	go func() {
		defer wg.Done()
		c.scanLoginOutput(stderr, parser)
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	if parser.Err() != nil {
		return parser.Err()
	}
	if errors.Is(loginCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("kiro-cli login timed out: %w", loginCtx.Err())
	}
	if loginCtx.Err() != nil {
		return fmt.Errorf("kiro-cli login canceled: %w", loginCtx.Err())
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
		args = append(args,
			"--license", "pro",
			"--identity-provider", c.identityURL,
			"--region", c.region,
		)
	case config.AuthGoogle:
		args = append(args, "--social", "google")
	case config.AuthGitHub:
		args = append(args, "--social", "github")
	case config.AuthBuilderID:
		args = append(args, "--license", "free")
	}
	return append(args, "--use-device-flow")
}

func (c *Client) scanLoginOutput(r io.Reader, parser *DeviceFlowParser) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if c.logger != nil {
			c.logger.Printf("kiro-cli: %s", sanitizeLogLine(line))
		}
		parser.Feed(line)
	}
	if err := scanner.Err(); err != nil && c.logger != nil {
		c.logger.Printf("reading kiro-cli output: %v", err)
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func sanitizeLogLine(line string) string {
	// Device codes are short-lived credentials. Keep them out of the daemon log.
	clean := ansiRE.ReplaceAllString(line, "")
	if strings.Contains(strings.ToLower(clean), "code:") {
		return "[device code emitted; redacted]"
	}
	if strings.Contains(strings.ToLower(clean), "open this url:") {
		return "[device login URL emitted; redacted]"
	}
	return clean
}
