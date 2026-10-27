package kiro

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
)

// CheckCapabilities probes only help, never the vault. The KAS auth verbs were
// introduced after KiroCrew v0.10.0, in upstream commit d199f301 (2026-10-26).
// Check capabilities rather than a version string: development builds may share
// a version number with a release that does not have these commands.
func (c *Client) CheckCapabilities(ctx context.Context) error {
	if c.backend != config.BackendKAS {
		return nil
	}
	commandCtx, cancel := context.WithTimeout(ctx, c.commandTimeout)
	defer cancel()
	out, err := c.command(commandCtx, "--help").CombinedOutput()
	if commandCtx.Err() != nil {
		return fmt.Errorf("probe kirocrew kas --help interrupted: %w", commandCtx.Err())
	}
	if err != nil {
		// Do not include raw subprocess output in a daemon error/log.
		return fmt.Errorf("probe kirocrew kas --help failed (KAS auth subcommands are required): %w", err)
	}
	if !kasAuthCommandsAvailable(string(out)) {
		return fmt.Errorf("KCLW_BACKEND=kas requires kirocrew kas login/whoami/logout; the configured executable does not advertise them (KiroCrew v0.10.0 lacks these commands). Use a build including upstream d199f301, or sign in through the KiroCrew dashboard; no authentication command was run")
	}
	return nil
}

var kasCommandChoicesRE = regexp.MustCompile(`\{([^{}]+)\}`)

func kasAuthCommandsAvailable(help string) bool {
	clean := ansiRE.ReplaceAllString(help, "")
	// argparse exposes subcommands as one comma-separated choice set. Require
	// all three in the SAME set, not words in prose or a successful help exit.
	for _, match := range kasCommandChoicesRE.FindAllStringSubmatch(clean, -1) {
		choices := make(map[string]bool)
		for _, choice := range strings.Split(match[1], ",") {
			choices[strings.TrimSpace(choice)] = true
		}
		if choices["login"] && choices["whoami"] && choices["logout"] {
			return true
		}
	}
	return false
}
