# PR #1: lifecycle, authentication and CI hardening

## CI root cause

The original macOS job (102716257776, run 34427673564) installed Go 1.22.12
successfully but macOS 26.6.2 aborted `watchdog.test` in dyld with
`missing LC_UUID load command`. This was a toolchain/loader incompatibility, not
a failing Go assertion. Commit c6ff2f0 already changed CI to Go 1.27.x and disabled
matrix fail-fast; that working change is retained. CI now adds formatting checks,
race detection, and real subprocess lifecycle tests on all three operating systems.
The module language minimum remains Go 1.22; use a supported Go toolchain on current OS releases.

## Instance ownership and graceful shutdown

`start`, `run`, and `once` share an OS-backed lifetime lock at `<PID file>.lock`.
`start` and `stop` additionally serialize transitions using `<PID file>.control.lock`.
The child publishes readiness only after acquiring its own lifetime lock. A launcher
reports success only after receiving an authenticated response from that child.

The PID file is now a private JSON control record with a PID, a literal loopback
address, and a cryptographically random 256-bit instance token. PID numbers are
informational: `status` and `stop` never probe or signal arbitrary numeric PIDs.
A stale file pointing to a different process cannot authorize stopping it.
Windows no longer relies on `OpenProcess` succeeding to infer liveness.

Control traffic uses a loopback-only HTTP listener with authenticated status/stop
endpoints, bounded timeouts, no redirects, and no environment proxy. A successful
stop request cancels the watchdog context, kills/reaps an active direct Kiro CLI
child through `exec.CommandContext`, and only then releases the lifetime lock.
It does not promise cleanup of arbitrary grandchildren or OS-service persistence.

**Upgrade from the original PID-only implementation:** stop an old running daemon
with the old executable before replacing it. Legacy PID files are deliberately
not trusted to stop processes; old daemons do not participate in the new lock.
Stale legacy files are safely replaced on the next start.

Keep the state directory local and user-private (0700 on Unix; a user-restricted
ACL on Windows). Do not delete either lock file while an instance is running:
file existence is not ownership, and unlinking a locked file can split ownership.
An occupied lock with an unavailable control endpoint is reported as an error,
not treated as permission to kill a PID or start another instance.

## Authentication and output handling

A successful `whoami` remains a health indication, not a guarantee that the upstream
M365 entitlement will authorize every subsequent operation. Only an explicit known
`not logged in` diagnostic from a normally exiting command triggers reactive login.
Unknown CLI errors, invalid flags, missing executables, deadlines, and service
failures are returned as errors. No undocumented dedicated exit code is assumed.
New/unrecognized CLI diagnostics fail closed and may require a parser update.

Device codes are extracted from both ordinary URL queries and IAM Identity Center
fragment queries such as `#/device?user_code=...`. The parser and log sanitizer use
the same credential recognizers, including `Code :`, ANSI formatting, alternate URL
prefixes, and multiple URLs on a line. Credential-bearing lines are not logged.
`os/exec` owns stdout/stderr drain goroutines; final unterminated output is processed
before returning. Excessively long lines fail safely rather than hanging the login.

Telegram failures report sanitized errors and numeric HTTP status, never untrusted
response bodies, response status text, or a request URL containing the bot token.

## Dynamic-exec review

The daemon re-executes only `os.Executable()` with the fixed `run` argument. Kiro
commands use a trusted local `KCLW_KIRO_BIN` executable and separate argv values;
there is no shell interpolation, command-string splitting, or executable selected
from CLI output, device URLs, or Telegram input. Startup checks resolve the executable
and fail before daemonization when it is unavailable. A shell-looking identity URL
is covered by a subprocess test and remains one literal argument.

This is a local-configuration trust boundary, not a sandbox: an attacker who can
replace the executable, alter PATH, or modify the env file can choose code to run.
Protect those files and prefer an absolute Kiro executable path. The dynamic-exec
scanner findings were audited on these grounds, not treated as proof of injection
and not silenced globally.

## Regression coverage

Tests use self-reexecuting Go test helpers and mocked HTTP transports, not real
identity-provider or Telegram credentials. Coverage includes concurrent launch,
foreground/once contention, crash recovery, stale/recycled PID records, wrong-token
control, redirect/non-loopback rejection, stopping a live login child, pipe draining,
fragment URLs, redaction variants, non-authentication errors, callback cancellation,
output limits, and Telegram error reflection. Cross-building is not a substitute
for native runtime tests; the CI matrix runs the same tests on Linux/macOS/Windows.
