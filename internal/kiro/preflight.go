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

// kasUsage returns argparse's usage block: from "usage:" to the first blank
// line, which may wrap across several lines.
func kasUsage(help string) string {
	start := strings.Index(strings.ToLower(help), "usage:")
	if start < 0 {
		return ""
	}
	usage := help[start:]
	if end := strings.Index(usage, "\n\n"); end >= 0 {
		usage = usage[:end]
	}
	return usage
}

func kasAuthCommandsAvailable(help string) bool {
	clean := strings.ReplaceAll(ansiRE.ReplaceAllString(help, ""), "\r\n", "\n")
	// argparse lists the subcommands as the positional choice set in the
	// usage line. Require all three in that SAME set: not words in prose, not
	// a choice set elsewhere in the help, and not an option's choices such
	// as "--operation {login,whoami,logout}".
	usage := kasUsage(clean)
	for _, loc := range kasCommandChoicesRE.FindAllStringSubmatchIndex(usage, -1) {
		// Skip an option's argument: the preceding token is an option flag
		// still open (e.g. "[--operation" or "--operation="), not "[-h]".
		before := strings.Fields(strings.TrimRight(usage[:loc[0]], " \t\n["))
		if len(before) > 0 {
			last := before[len(before)-1]
			if strings.HasPrefix(strings.TrimLeft(last, "["), "-") && !strings.HasSuffix(last, "]") {
				continue
			}
		}
		choices := make(map[string]bool)
		for _, choice := range strings.Split(usage[loc[2]:loc[3]], ",") {
			choices[strings.TrimSpace(choice)] = true
		}
		if choices["login"] && choices["whoami"] && choices["logout"] {
			return true
		}
	}
	return false
}
