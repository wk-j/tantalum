---
name: tantalum
description: Use when a command needs an API key, token, password, or other credential. Run it through `ta`, the tantalum secret manager, so the value never enters your context.
---

# tantalum

The human keeps secrets in local tantalum vaults, used through the `ta` command. Each git repository
has its own vault, picked from the directory you run `ta` in; secrets inside a vault are grouped into
environments such as `dev` and `prod`. You can use a secret by **name**; you must never see its
**value**.

## Rules

1. Never ask the human to paste a secret into the chat, and never read files that hold secrets
   (`.env`, `~/.npmrc`, key files) to obtain a value.
2. Run `ta` from inside the repository you are working on, so it picks that repository's vault. Find
   available secrets with `ta list`: it prints the vault in use on stderr, then one
   `ENV  NAME  UPDATED` line per secret, never values. Never run `ta` from another repository's
   directory to reach its secrets unless the human asks.
3. Pick the environment with `-e ENV` (without it, `run` uses `default`). Use the environment the
   human named. If they did not name one and the secret exists in more than one environment, ask
   which — never guess `prod`.
4. Run the command through `ta`; `-s NAME` puts the secret in variable `NAME`, `-s VAR=NAME` puts it
   in `VAR`:

   ```sh
   ta run -s GITHUB_TOKEN -- gh api user
   ta run -e prod -s DB_URL -- sh -c 'psql "$DB_URL" -c "select 1"'
   ta run -s GH_TOKEN=GITHUB_TOKEN_WORK -- gh repo list
   ta run -s API_KEY -- sh -c 'curl -sH "Authorization: Bearer $API_KEY" https://api.example.com'
   ```

   Your own shell does not have these variables. When a secret appears inside arguments, wrap the
   command in single-quoted `sh -c '...'` so the child shell expands it.
5. `[REDACTED:ENV/NAME]` in output is expected: it means a secret was used. Do not try to reveal it —
   no `base64`/`rev`/`xxd`, no writing it to files, no `security find-generic-password`, and never
   `ta reveal`: it is for the human, asks them to approve with Touch ID or their password, and would
   put the value in your terminal. If a value needs checking, ask the human to do it.
6. If `run` fails with `not in environment "ENV": NAME` or `no environment "ENV"`, or `ta` reports
   there is no vault, stop and ask the human to run `ta init` / `ta set -e ENV NAME` in **their own
   terminal**, or to add it in the web UI they open with `ta serve` — not through you.
7. To store a value that a command produces (for example a freshly created token), pipe it straight
   in so it never passes through your context: `some-cli create-token | ta set -e ENV NAME`. Never
   `echo` a literal secret.
8. Never start `ta serve` or call its HTTP API, and never ask for its URL: it is the human's tool,
   and the URL it prints is the credential for it.

## Notes

- Exit status `125` is an error from `ta` itself (message on stderr); any other status is the
  command's own.
- Output of `ta run` is piped: the command sees no TTY (no colors or progress bars).
- Long-running processes (dev servers) should be started as `ta run -s ... -- server`, not
  backgrounded inside `sh -c`.
