# kiro-cli-login-watchdog

A small self-managed daemon that keeps Kiro CLI authentication usable on machines where enterprise identity sessions expire regularly.

The first-stage flow is deliberately simple:

1. Run `kiro-cli whoami` on an internal interval.
2. If Kiro CLI is no longer authenticated, start a device-flow login.
3. Parse the one-time device code and browser URL from Kiro CLI output.
4. Send the code and URL to Telegram.
5. Keep the Kiro CLI login process running while you finish the browser sign-in remotely.
6. Verify the recovered login with `kiro-cli whoami`.

No system crontab entry is required. The watchdog has its own scheduler and a detached daemon mode.

## Security note

Never put a Telegram bot token in source code, shell history, a public URL, or a Git commit. Configure it through environment variables or a mode-0600 env file. If a token has already been exposed, revoke/regenerate it with BotFather before using this project.

Device codes and device-login URLs are also treated as short-lived credentials. The daemon redacts them from its log; they are sent only through the configured notifier.

## Current authentication support

| Method | `KCLW_AUTH_METHOD` | Kiro CLI invocation |
| --- | --- | --- |
| AWS IAM Identity Center | `identity-center` | `login --identity-provider ... --region ... --use-device-flow` |
| Google | `google` | `login --social google --use-device-flow` |
| GitHub | `github` | `login --social github --use-device-flow` |
| Builder ID | `builder-id` | `login --license free --use-device-flow` |

The first-stage target is IAM Identity Center with a known `awsapps.com/start` URL. The Identity Center command intentionally omits `--license pro` because the operator-verified Kiro CLI build fails to construct the request when that flag is combined with the explicit identity provider. Kiro's generic **Your Organization** discovery-by-email flow is intentionally left for a later phase.

## Build

Requires Go 1.22 or newer.

```bash
go build -o kiro-cli-login-watchdog ./cmd/kiro-cli-login-watchdog
```

## Configure

Copy the example file and keep the real copy out of Git:

```bash
cp .env.example .env
chmod 600 .env
```

For IAM Identity Center:

```dotenv
KCLW_KIRO_BIN=kiro-cli
KCLW_AUTH_METHOD=identity-center
KCLW_IDENTITY_PROVIDER=https://d-example.awsapps.com/start
KCLW_REGION=ap-northeast-1
KCLW_CHECK_INTERVAL=5m
KCLW_FORCE_RELOGIN_INTERVAL=0
KCLW_TELEGRAM_BOT_TOKEN=replace-with-a-new-token
KCLW_TELEGRAM_CHAT_ID=replace-me
```

Environment variables already present in the process take precedence over values in `--env-file`.

## Test once

Run one health check in the foreground before starting the daemon:

```bash
./kiro-cli-login-watchdog once --env-file .env
```

If you are logged out, Telegram should receive a message similar to:

```text
Kiro CLI login required

Code: ABCD-EFGH
Open: https://example.awsapps.com/start/#/device?user_code=ABCD-EFGH
```

Finish that device login in a browser. The process waits for Kiro CLI to complete and then verifies `whoami` again.

## Daemon mode

Start detached in the background:

```bash
./kiro-cli-login-watchdog start --env-file .env
```

Check status:

```bash
./kiro-cli-login-watchdog status --env-file .env
```

Stop it:

```bash
./kiro-cli-login-watchdog stop --env-file .env
```

Run in the foreground only when debugging or when a process supervisor already owns daemonization:

```bash
./kiro-cli-login-watchdog run --env-file .env
```

The daemon writes its state and private append-only log under the configured state directory. On Linux the default is `~/.local/state/kiro-cli-login-watchdog`; macOS and Windows use the platform user configuration directory. Paths can be overridden with `KCLW_STATE_DIR`, `KCLW_PID_FILE`, and `KCLW_LOG_FILE`.

## Scheduling strategy

The default is reactive rather than destructive: check `whoami` every 5 minutes and only start a new login after authentication becomes unhealthy. For an entitlement that expires at approximately 24 hours, this normally limits downtime to the check interval without deliberately invalidating a still-good Kiro session.

If you prefer a guaranteed daily re-login, set for example:

```dotenv
KCLW_FORCE_RELOGIN_INTERVAL=23h30m
```

The daemon will then proactively run `kiro-cli logout` followed by the configured device-flow login after that interval. The force timer starts when the daemon starts and resets after a successful watchdog-driven login.

## Commands

```text
kiro-cli-login-watchdog start  [--env-file .env]
kiro-cli-login-watchdog stop   [--env-file .env]
kiro-cli-login-watchdog status [--env-file .env]
kiro-cli-login-watchdog run    [--env-file .env]
kiro-cli-login-watchdog once   [--env-file .env]
kiro-cli-login-watchdog version
```

## Environment variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `KCLW_KIRO_BIN` | `kiro-cli` | Kiro CLI executable |
| `KCLW_AUTH_METHOD` | `identity-center` | Authentication strategy |
| `KCLW_IDENTITY_PROVIDER` | - | Identity Center Start URL |
| `KCLW_REGION` | - | Identity Center region |
| `KCLW_CHECK_INTERVAL` | `5m` | Internal watchdog interval |
| `KCLW_FORCE_RELOGIN_INTERVAL` | `0` | Optional proactive logout/login interval |
| `KCLW_COMMAND_TIMEOUT` | `20s` | Timeout for short Kiro commands |
| `KCLW_LOGIN_TIMEOUT` | `15m` | Maximum time to wait for device login |
| `KCLW_TELEGRAM_BOT_TOKEN` | - | Telegram bot token |
| `KCLW_TELEGRAM_CHAT_ID` | - | Telegram destination chat ID |
| `KCLW_NOTIFY_SUCCESS` | `true` | Send a second message after login is restored |
| `KCLW_STATE_DIR` | platform default | PID/log directory |
| `KCLW_PID_FILE` | under state dir | Override state record path |
| `KCLW_LOG_FILE` | under state dir | Override log file |

## Design for later phases

Authentication command construction is isolated in `internal/kiro`, and notification delivery is behind a small `Notifier` interface. That leaves the next changes localized rather than coupling them to the scheduler/daemon code.

Planned extensions:

- Additional notification backends: generic webhook, Slack, Discord, email, etc.
- Multiple notifiers at once with per-backend retry policy.
- Stable automation of Kiro's generic **Your Organization** discovery flow when a non-interactive CLI selector is available.
- Native service/autostart installation for systemd, launchd, and Windows rather than only self-daemonization.
- Persisted scheduling metadata if proactive re-login timing must survive daemon restarts exactly.

## Development

```bash
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
```

CI runs formatting, tests, the race detector, vet, builds on Linux/macOS/Windows, and a dedicated `golangci-lint` job on Ubuntu.

The project has no third-party Go runtime dependencies in phase 1.
