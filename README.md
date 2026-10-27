# kiro-cli-login-watchdog

A small self-managed daemon that keeps **Kiro CLI** or **KiroCrew KAS (kiro-agent)** authentication usable on machines where enterprise identity sessions expire regularly.

The flow is deliberately simple:

1. Run the selected backend's `whoami` on an internal interval.
2. If it explicitly reports signed out, start a device-flow login.
3. Parse the one-time device code and browser URL from the login output.
4. Send the code and URL to Telegram.
5. Keep the login process running while you finish the browser sign-in remotely.
6. Verify the recovered login with the same backend's `whoami`.

No system crontab entry is required. The watchdog has its own scheduler and a detached daemon mode.

## Security note

Never put a Telegram bot token in source code, shell history, a public URL, or a Git commit. Configure it through environment variables or a mode-0600 env file. If a token has already been exposed, revoke/regenerate it with BotFather before using this project.

Device codes and device-login URLs are also treated as short-lived credentials. The daemon redacts them from its log; they are sent only through the configured notifier.

## Authentication backends

`KCLW_BACKEND=kiro-cli` (the default) retains the original behavior and uses `KCLW_KIRO_BIN`. `KCLW_BACKEND=kas` uses `KCLW_KIROCREW_BIN` and runs `kirocrew kas whoami`, `kirocrew kas login ...`, and `kirocrew kas logout`. No backend is auto-detected, and a failed KAS command never falls back to kiro-cli.

KAS is the agent engine; **KiroCrew owns its vault authentication**. Do not point the watchdog at `kiro-agent` or put `kirocrew kas` in an executable setting. The executable path is a single argv element, so paths containing spaces work. The watchdog does not read, copy, or modify tokens or vault files itself.

Use a KiroCrew build providing the `kas whoami`, `kas login`, and `kas logout` subcommands. This integration follows the [upstream CLI contract](https://github.com/kirodotdev/KiroCrew/blob/144978bb9a61f1b6ff882813dcfdfc2677ce99e1/src/kiro_crew/cli_kas_login.py). Unsupported-command errors are operational failures, not signals to start login. A KAS relay still authenticating through kiro-cli should keep `KCLW_BACKEND=kiro-cli`; select `kas` when the KiroCrew vault owns the sign-in.

## Current authentication support

| Method | `KCLW_AUTH_METHOD` | `kiro-cli` arguments | `kirocrew` arguments |
| --- | --- | --- | --- |
| AWS IAM Identity Center | `identity-center` | `login --identity-provider ... --region ... --use-device-flow` | `kas login --provider idc --start-url ... --region ...` |
| Google | `google` | `login --social google --use-device-flow` | `kas login --provider google` |
| GitHub | `github` | `login --social github --use-device-flow` | `kas login --provider github` |
| Builder ID | `builder-id` | `login --license free --use-device-flow` | `kas login --provider builder_id` |

KiroCrew's `kas login` already uses a device flow; legacy `--use-device-flow`, `--license`, and `--social` flags must not be passed to it.

The original target is IAM Identity Center with a known `awsapps.com/start` URL. The Identity Center command intentionally omits `--license pro` because the operator-verified Kiro CLI build fails to construct the request when that flag is combined with the explicit identity provider. Kiro's generic **Your Organization** discovery-by-email flow is intentionally left for a later phase.

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

For Kiro CLI with IAM Identity Center:

```dotenv
KCLW_BACKEND=kiro-cli
KCLW_KIRO_BIN=kiro-cli
KCLW_AUTH_METHOD=identity-center
KCLW_IDENTITY_PROVIDER=https://d-example.awsapps.com/start
KCLW_REGION=ap-northeast-1
KCLW_CHECK_INTERVAL=5m
KCLW_FORCE_RELOGIN_INTERVAL=0
KCLW_TELEGRAM_BOT_TOKEN=replace-with-a-new-token
KCLW_TELEGRAM_CHAT_ID=replace-me
```

For KiroCrew KAS, change these two settings; the Identity Center and Telegram settings above still apply:

```dotenv
KCLW_BACKEND=kas
KCLW_KIROCREW_BIN=kirocrew
# Set this only if KiroCrew uses a non-default data home:
# KIROCREW_HOME=/path/to/your/crew-home
```

For Builder ID, set `KCLW_AUTH_METHOD=builder-id`; a start URL and region are not required. Google and GitHub use `google` and `github`, respectively.

Run the watchdog as the **same OS user**, with the same `HOME` and `KIROCREW_HOME` environment as the KiroCrew gateway. Child processes and the detached daemon inherit that environment. A login to a different home does not repair the gateway's vault. Check the target first with `kirocrew kas whoami`.

Stop an existing daemon **before** changing backend or executable settings, then start it with the updated env file. To monitor both credential stores separately, run two configurations with distinct `KCLW_STATE_DIR` values (and distinct PID/log paths if explicitly overridden).

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

KAS notifications use the heading `KiroCrew KAS login required`. Finish that device login in a browser. The process waits for the selected backend to complete and then verifies its `whoami` again.

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

The default is reactive rather than destructive: check `whoami` every 5 minutes and start a new login only after the selected backend explicitly reports signed out. Command failures do not trigger login or logout.

For KAS, `whoami` is a **vault-status check**, not an active model request or entitlement probe. Upstream checks whether the stored identity is usable and whether refresh has previously been rejected; it does not force an online refresh. This watchdog cannot guarantee detection of every server-side revocation before KiroCrew observes it. Only exit 1 with the complete diagnostic `Not signed in to Kiro Crew.` starts a reactive KAS login. Upstream also reports an unreadable vault as signed out; inspect vault permissions if re-login cannot finish.

For an intentional proactive re-login interval, set for example:

```dotenv
KCLW_FORCE_RELOGIN_INTERVAL=23h30m
```

The daemon will then run the selected backend's logout (`kiro-cli logout` or `kirocrew kas logout`) followed by the configured device-flow login after that interval. The timer starts when the daemon starts and resets after a successful watchdog-driven login; it is not the token's actual expiry time. Keep this disabled unless intentionally rotating the sign-in: **`kirocrew kas logout` removes all identities in that KiroCrew vault**.

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
| `KCLW_BACKEND` | `kiro-cli` | Authentication owner: `kiro-cli` or `kas` |
| `KCLW_KIRO_BIN` | `kiro-cli` | Executable used only by the `kiro-cli` backend |
| `KCLW_KIROCREW_BIN` | `kirocrew` | Executable used only by the `kas` backend |
| `KIROCREW_HOME` | KiroCrew default | Optional inherited data home; must match the gateway |
| `KCLW_AUTH_METHOD` | `identity-center` | Authentication strategy |
| `KCLW_IDENTITY_PROVIDER` | - | Identity Center Start URL |
| `KCLW_REGION` | - | Identity Center region |
| `KCLW_CHECK_INTERVAL` | `5m` | Internal watchdog interval |
| `KCLW_FORCE_RELOGIN_INTERVAL` | `0` | Optional proactive logout/login interval |
| `KCLW_COMMAND_TIMEOUT` | `20s` | Timeout for short authentication commands |
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
