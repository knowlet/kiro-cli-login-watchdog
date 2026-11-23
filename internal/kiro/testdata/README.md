# KiroCrew CLI compatibility fixtures

The help fixtures were generated from upstream's actual `add_kas_parser`
function, with `argparse.ArgumentParser(prog="kirocrew kas")`, on 2026-10-27.
The engine version constants were supplied locally; engine installation, the
application boot path, and authentication services were not run.

- `kirocrew-v0.10.0-kas-help.txt`: `src/kiro_crew/cli_kas.py` at tag `v0.10.0`.
- `kirocrew-d199f301-kas-help.txt`: the same parser plus
  `cli_kas_login.add_kas_login_parsers` at commit
  `d199f301a1c2708cd0896a4a3e6b537ec8b8035b` (PR #23657).

The real parser definitions reject `login --help`, `whoami --help`, and
`logout --help` with exit 2 on v0.10.0 and accept each with exit 0 at d199f301.
These fixtures document the release/development distinction; they are not proof
of a live device-flow login. Subprocess lifecycle tests still use test doubles.
