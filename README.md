# tantalum

Local secret manager for AI coding agents (macOS). The command is `ta`, tantalum's chemical symbol.

Agents need API keys and passwords to run commands, but everything an agent reads — `.env` files,
command output, text pasted into chat — lands in its context, the model provider's logs, and session
transcripts. tantalum lets an agent **use** a secret by name without ever **seeing** its value.

- **Vault**: `~/.tantalum/vault.json`, AES-256-GCM. Secret names are encrypted too.
- **Projects**: inside a git repository, `ta` uses that repository's own vault,
  `~/.tantalum/projects/NAME-HASH`, keyed by the repository directory. Nothing to name or configure.
- **Environments**: secrets are grouped into named environments such as `dev` and `prod`; the same
  name can hold a different value in each.
- **Key**: a random 256-bit key per vault in the macOS login Keychain (service `tantalum`, account =
  vault directory).
- **`run`**: starts a command with one environment's secrets as variables and pipes its stdout/stderr
  through a redactor that replaces every value of the vault in use with `[REDACTED:ENV/NAME]`.
- **`serve`**: a built-in web UI on `127.0.0.1` where a human adds, replaces and deletes secrets.
  Values are write-only there too.

## Install

```sh
go install ./cmd/ta    # -> $(go env GOPATH)/bin/ta
```

## Human: manage secrets

```sh
ta init                        # once: create vault + Keychain key
ta set GITHUB_TOKEN            # hidden prompt; paste the value, press Enter
ta set -e prod DB_URL          # store in environment prod
some-cli create-token | ta set X   # or pipe it; one trailing newline is dropped
ta set TLS_KEY < key.pem       # multi-line values go through stdin
ta list                        # ENV, NAME and last update per line, never values
ta rm -e prod DB_URL
```

`set` never accepts the value as an argument, so it stays out of shell history and `ps`.
Flags come before `NAME` (`ta set -e prod DB_URL`, not `ta set DB_URL -e prod`).
Secret names must be valid shell variable names (`[A-Za-z_][A-Za-z0-9_]*`).

### Environments

`-e ENV` on `set`, `rm` and `run` picks the environment; without it they use `default`. An environment
exists while it holds at least one secret: `ta set -e staging X` creates `staging`, and removing its
last secret removes it. Environment names use letters, digits, `_` and `-`, starting with a letter or
digit.

Vaults created before environments existed (file `"version": 1`) keep working: their secrets load as
environment `default`, and the next change rewrites the file as version 2. A `ta` built before
environments cannot read version 2, so reinstall with `go install ./cmd/ta`.

### Projects

Inside a git repository, `ta` uses a vault that belongs to that repository:

```sh
cd ~/src/api
ta init                        # once per repository: creates ~/.tantalum/projects/api-<hash>
ta set -e prod DB_URL          # stored in the api repository's vault
```

The vault is keyed by the repository directory (the one holding `.git`). Every subdirectory and every
`git worktree` of the repository uses the same vault, and so does the same directory reached through a
symlink or with different letter case. Outside any repository, commands use the default vault in
`~/.tantalum`. `ta list` and `ta serve` print which vault they use.

Because the key is the directory, another clone is another project with its own vault, and moving or
renaming the repository directory starts a new, empty vault. The old one stays in
`~/.tantalum/projects/NAME-HASH` until you remove it.

### Web UI

```sh
ta serve                       # loopback only, default 127.0.0.1:7340; -addr HOST:PORT to change
```

Open the printed `http://127.0.0.1:7340/#token=…` URL. The page shows one tab per environment with
names and update times, saves a new or replacement value in any environment (type a new environment
name to create it; one trailing newline from a paste is dropped), and deletes secrets. It never shows a
value: no endpoint returns one. Ctrl-C stops the server. The header shows which vault the page manages;
for several projects, run one `ta serve` per project, each with its own `-addr` port.

Start `ta serve` in your own terminal, not through an agent: the printed URL is the key to the UI, and
each run prints a new one.

The page uses this JSON API; every call needs `Authorization: Bearer <token>`:

| Request                            | Body               | Response                                                  |
| ---------------------------------- | ------------------ | --------------------------------------------------------- |
| `GET /api/vault`                   |                    | `200` `{"root": "...", "dir": "..."}` (`root` `""` = default vault) |
| `GET /api/secrets`                 |                    | `200` `[{"env": "...", "name": "...", "updated": "..."}]` |
| `PUT /api/secrets/{env}/{name}`    | `{"value": "..."}` | `204`; `400` bad environment, name, empty value or body   |
| `DELETE /api/secrets/{env}/{name}` |                    | `204`; `404` not in the vault                             |

Errors are `{"error": "..."}`.

## Agent: use secrets

```sh
ta list
ta run -s GITHUB_TOKEN -- gh api user                      # environment default
ta run -e prod -s DB_URL -- sh -c 'psql "$DB_URL" -c "select 1"'
ta run -s GH_TOKEN=GITHUB_TOKEN_WORK -- gh repo list      # VAR=NAME renames the variable
ta run -s API_KEY -- sh -c 'curl -sH "Authorization: Bearer $API_KEY" https://api.example.com'
```

The agent's own shell does not have `$API_KEY`; reference it inside single-quoted `sh -c '...'` so the
child shell expands it.

### Connect your agent

`skills/tantalum/SKILL.md` tells an agent how to work with `ta`. Install it where your agent loads
skills, for example:

```sh
ln -s "$PWD/skills/tantalum" ~/.agents/skills/tantalum     # omp
ln -s "$PWD/skills/tantalum" ~/.claude/skills/tantalum     # Claude Code
```

Agents without skill support: paste the rules section of that file into `AGENTS.md` / `CLAUDE.md`.

## Threat model

Protects against:

- Secret values reaching the agent's context, provider logs, or transcripts through command output.
- Plaintext secrets in `.env` files that an agent can read or commit.
- A copied vault file (backups, sync): it is useless without the Keychain key.

Does **not** protect against a deliberately hostile agent. The agent runs code as your user, so it can
transform a value before printing it (`base64`, `rev`), write it to a file, send it over the network, or
read the key with `security find-generic-password`. Redaction matches literal values on stdout/stderr
only; it does not see what the child writes to `/dev/tty` or to files. To contain an untrusted agent,
run it as a separate OS user or inside a sandbox/VM.

The web UI listens on loopback only and creates a new 256-bit token per run. The token travels in the
URL fragment, which the browser never sends to the server, and the page keeps it in the tab's
`sessionStorage`. Requests whose `Host` or `Origin` is not the server itself are rejected, which blocks
other websites and DNS rebinding, and pages are served with a strict Content-Security-Policy. A local
process that gets the token can add, replace or delete secrets, but cannot read a value.

Picking the vault from the working directory grants nothing: anyone who can run `ta` can run it in any
repository's directory.

## Behavior notes

- `run` redacts **every** value in the vault it uses, in every environment, not only the injected ones
  (catches a token that also sits in a config file the command prints, or a `prod` value while running
  with `dev`). Other projects' vaults are not loaded, so their values are not redacted. A value shorter
  than a few characters will be redacted wherever it appears in output.
- A secret split across two output chunks is still caught: bytes that could start a secret are held
  until the next chunk or the end of the stream.
- The child's stdout/stderr are pipes, so it sees no TTY: expect no colors or progress bars and possible
  output buffering. stdin is passed through unchanged.
- `set`, `rm` and web UI writes take an exclusive lock and replace the vault atomically (temp file,
  fsync, rename), so parallel agents, terminals and the web UI do not lose each other's changes.
- The hidden prompt reads raw input, so pasted values longer than macOS's 1024-byte line limit are not
  truncated. Ctrl-C or Ctrl-D cancels without storing.

Exit status: errors from `ta` itself exit `125`. `run` exits with the command's status: `126` not
executable, `127` not found, `128+N` killed by signal N. SIGINT, SIGTERM, SIGHUP and SIGQUIT are
forwarded to the command.

`TANTALUM_HOME` moves the default vault and `projects/` (default `~/.tantalum`).

### Remove a vault

```sh
# one project: `ta list` inside the repository prints its vault directory
rm -rf ~/.tantalum/projects/api-1a2b3c4d5e6f
security delete-generic-password -s tantalum -a "$HOME/.tantalum/projects/api-1a2b3c4d5e6f"

# the default vault (project vaults stay)
rm -f ~/.tantalum/vault.json ~/.tantalum/vault.lock
security delete-generic-password -s tantalum -a "$HOME/.tantalum"
```

## Development

```sh
go test ./...
```
