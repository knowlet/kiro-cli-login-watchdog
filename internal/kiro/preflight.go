package kiro

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/knowlet/kiro-cli-login-watchdog/internal/config"
)

// kasHelpProbeLimit is the most help text the capability probe will buffer.
// Real kas --help output is a few kilobytes; anything larger is rejected
// instead of being parsed from a truncated stream.
const kasHelpProbeLimit = 256 * 1024

var errKASHelpTooLarge = errors.New("kas help probe output exceeded the size limit")

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
	out, err := c.readKASHelp(commandCtx)
	if commandCtx.Err() != nil {
		return fmt.Errorf("probe kirocrew kas --help interrupted: %w", commandCtx.Err())
	}
	if errors.Is(err, errKASHelpTooLarge) {
		return err
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

// readKASHelp runs kas --help in the C locale. argparse translates the
// "usage:" heading through gettext, and LANGUAGE overrides LC_ALL, so a
// localized parent would hide the subcommand list from kasUsage.
func (c *Client) readKASHelp(ctx context.Context) ([]byte, error) {
	cmd := c.command(ctx, "--help")
	cmd.Env = cLocaleEnv(os.Environ())
	return combinedOutputLimited(ctx, cmd, kasHelpProbeLimit)
}

// cLocaleEnv forces English gettext messages for the help probe.
func cLocaleEnv(base []string) []string {
	filtered := make([]string, 0, len(base)+1)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok && (strings.EqualFold(key, "LANGUAGE") || strings.EqualFold(key, "LC_ALL")) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "LC_ALL=C")
}

// combinedOutputLimited merges stdout and stderr and stops after limit bytes.
// A longer stream is an error. Cancel closes the read end before Wait: a
// descendant that inherited the write end would otherwise keep ReadAll
// blocked after the direct child has been killed.
func combinedOutputLimited(ctx context.Context, cmd *exec.Cmd, limit int) ([]byte, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err = cmd.Start(); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, err
	}
	_ = writer.Close()

	readDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// Unblock ReadAll even if a grandchild still holds the write end.
			_ = reader.Close()
		case <-readDone:
		}
	}()
	buf, readErr := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	close(readDone)
	_ = reader.Close()

	exceeded := len(buf) > limit
	if (exceeded || ctx.Err() != nil) && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if exceeded {
		return nil, fmt.Errorf("probe kirocrew kas --help output exceeded %d bytes: %w", limit, errKASHelpTooLarge)
	}
	if readErr != nil {
		return nil, readErr
	}
	return buf, waitErr
}

// kasUsage returns argparse's usage block: from "usage:" to the first blank
// line, which may wrap across several lines. The help probe forces the C
// locale so this English heading is what argparse prints.
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
	// Depth is carried forward so each byte of a large help text is visited
	// once. Rescanning the prefix per choice set is quadratic.
	depth := 0
	pos := 0
	for _, loc := range kasCommandChoicesRE.FindAllStringSubmatchIndex(usage, -1) {
		// argparse formats subparsers as "{a,b} ..." at bracket depth zero.
		// An option's choices stay inside its [] group, including nargs forms:
		// nargs=N repeats "{a,b}", nargs=+ is "{a,b} [{a,b} ...]", and nargs=*
		// or ? nests the set as "[{a,b} ...]" or "[{a,b}]". A repeat is not a
		// subcommand list just because the flag is no longer the previous token.
		for pos < loc[0] {
			switch usage[pos] {
			case '[':
				depth++
			case ']':
				if depth > 0 {
					depth--
				}
			}
			pos++
		}
		if depth != 0 {
			continue
		}
		rest := strings.TrimLeft(usage[loc[1]:], " \t\r\n")
		if !strings.HasPrefix(rest, "...") {
			continue
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
