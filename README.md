# tantalum

Local secret manager for AI coding agents on macOS. The command is `ta`.

Anything an agent reads — `.env` files, command output, pasted text — lands in its context, the
provider's logs and session transcripts. With `ta`, an agent uses a secret by **name** and never sees
its **value**.

## Install

```sh
git clone https://github.com/wk-j/tantalum.git && cd tantalum
go install ./cmd/ta    # needs the Xcode command line tools (cgo for Touch ID and the clipboard)
```

## Human: manage secrets

```sh
ta init                            # once per vault: creates it and its Keychain key
ta set GITHUB_TOKEN                # hidden prompt; the value is never an argument
ta set -e prod DB_URL              # environment prod (default: "default")
some-cli create-token | ta set X   # or pipe it in; one trailing newline is dropped
ta import .env                     # import into default; keep existing secrets
ta import -e prod .env.production  # import into one environment
ta import -e dev -overwrite .env   # explicitly replace existing secrets
ta import -e dev - < .env          # read the same format from stdin
ta export secrets.ta-export        # after Touch ID/password: every secret, encrypted with a passphrase you type
ta import secrets.ta-export        # on another Mac or vault: asks for that passphrase
ta list                            # ENV, NAME, last update; never values
ta rm -e prod DB_URL
ta reveal [-e ENV] [-c] NAME       # after Touch ID/password: print to the terminal, or -c to clipboard
ta serve                           # web UI on 127.0.0.1:7340, or a free port if taken; open the printed URL
```

Flags go before `NAME` or `FILE`; names are shell variable names (`[A-Za-z_][A-Za-z0-9_]*`). Run `reveal`, `export` and
`serve` in your own terminal, not through an agent: the revealed value appears on that terminal, the export
passphrase is typed there, and the URL `serve` prints is the key to the UI.

### Import a .env file

`ta import [-e ENV] [-overwrite] FILE` reads a UTF-8 file directly into the vault selected by your
working directory; use `-` as `FILE` to read piped or redirected stdin (interactive terminal input is
refused so pasted secrets cannot be echoed). It prints only imported/kept counts, never values.
Existing secrets and their update times are kept unless `-overwrite` is given. The source file is not
changed or deleted, so remove or otherwise protect your plaintext copy yourself after checking the import.

The format supports `NAME=VALUE`, optional `export`, blank lines and `#` comments. Unquoted values are
trimmed; a `#` at the start of the value or after a space/tab begins a comment. Single-quoted values are
literal. Double-quoted values decode `\n`, `\r`, `\t`, `\"` and `\\`; other backslash sequences stay
literal. Both quote styles can span lines and preserve quoted whitespace. Shell commands and variable
references such as `$HOME`, `${TOKEN}` and `$(command)` are **not** executed or expanded. Later duplicate
names win within the input file.

The complete input is validated before a single locked, encrypted vault write: a malformed entry,
unclosed quote, text after a closing quote (other than a comment), empty value, NUL byte, invalid UTF-8,
or file with no secrets fails without importing anything. Parse errors show line numbers/reasons, not
source text or values.

### Move a vault: export and import

`ta export FILE` writes every environment of the vault selected by your working directory to a new file,
encrypted with a passphrase you choose. It first waits for the same Touch ID/password prompt as `ta reveal`,
then asks for the passphrase twice on the terminal (at least 12 characters). It never overwrites a file and
never writes to stdout; the file is created with mode 0600 and does not contain the vault's Keychain key.

`ta import [-overwrite] FILE` recognises an export file and asks for its passphrase on the terminal, so it
works in any vault on any Mac. It restores the environments the file holds (`-e` is refused for export
files) and keeps the secrets' update times. As with `.env` files, existing secrets are kept unless
`-overwrite` is given, and a wrong passphrase, damaged file or invalid entry imports nothing.

The file is only as strong as its passphrase: anyone holding a copy can try passphrases offline. Use a long
one, keep it apart from the file, and delete the file once it has served its purpose.

## Agent: use secrets

```sh
ta run -s GITHUB_TOKEN -- gh api user
ta run -e prod -s DB_URL -- sh -c 'psql "$DB_URL" -c "select 1"'
ta run -s GH_TOKEN=GITHUB_TOKEN_WORK -- gh repo list    # VAR=NAME renames the variable
```

`run` passes secrets to the command as environment variables and replaces every vault value in its
stdout/stderr with `[REDACTED:ENV/NAME]`. The variables exist only in the child, so reference them
inside single-quoted `sh -c '...'`.

Give your agent the rules in `skills/tantalum/SKILL.md`:

```sh
ln -s "$PWD/skills/tantalum" ~/.agents/skills/tantalum     # omp
ln -s "$PWD/skills/tantalum" ~/.claude/skills/tantalum     # Claude Code
```

Agents without skill support: paste its Rules section into `AGENTS.md` / `CLAUDE.md`.

## How it works

- **Vault**: `~/.tantalum/vault.json`, AES-256-GCM, names encrypted too. Each vault has a random
  256-bit key in the login Keychain (service `tantalum`, account = vault directory).
- **Projects**: inside a git repository, `ta` uses that repository's own vault,
  `~/.tantalum/projects/NAME-HASH`, keyed by the repository directory; subdirectories and worktrees
  share it, while another clone or a moved repository starts a new, empty one. Elsewhere `ta` uses the
  default vault. `TANTALUM_HOME` moves `~/.tantalum`.
- **Environments**: `-e ENV` on `set`, `import`, `rm`, `run` and `reveal`; one name can hold a different value per
  environment. An environment exists while it holds a secret.
- **Web UI**: a grid of names × environments to add, replace, import `.env` files, copy, rename and
  delete. No endpoint returns a value; a cell's **Copy value** puts it on the clipboard after the same
  prompt as `ta reveal -c`.
- Writes lock the vault and replace it atomically, so parallel agents, terminals and the web UI are safe.

## Threat model

Protects against secret values reaching an agent's context, logs or transcripts through command output;
plaintext `.env` files an agent can read or commit; and a copied vault file, useless without the
Keychain key.

Does **not** protect against a hostile agent: it runs as your user, so it can encode a value before
printing it, write it to a file, send it over the network, or read the key with
`security find-generic-password`. Contain one with a separate OS user or a sandbox/VM.

- Redaction matches literal values in what the command writes to stdout and stderr (and to its terminal
  when ta runs in one), not in files. It covers every environment of the vault in use, not other
  projects' vaults. Very short values are redacted wherever they appear, even inside colour codes.
- Run in a terminal, the command gets a pseudo-terminal of its own, so colours, progress bars, pagers,
  prompts and full-screen programs work as when run directly; Ctrl-Z suspends it together with `ta`.
  Otherwise (agents, pipelines, stdin redirected) it gets pipes: no colours or progress bars, possible
  output buffering.
- `export` waits for the same prompt and reads its passphrase from `/dev/tty`, so an agent without a terminal
  cannot start one. An export file is AES-256-GCM under a PBKDF2-HMAC-SHA256 key (600,000 iterations) of
  the passphrase; a weak passphrase can be guessed offline.
- `reveal` and the web UI's **Copy value** wait for a system prompt an agent cannot answer; approving one
  you did not start hands the value to whoever did. `reveal` writes to `/dev/tty`, never stdout. A
  clipboard copy is marked concealed and cleared after 45 seconds unless something else was copied.
- The web UI listens on loopback with a new 256-bit token per run, carried in the URL fragment. It
  rejects foreign `Host`/`Origin` headers and serves a strict CSP. A process holding the token can
  change secrets; to read one it can only request **Copy value**, which waits for the same prompt.

Exit status: `ta`'s own errors exit `125`; `run` exits with the command's status (`126` not executable,
`127` not found, `128+N` killed by signal N) and forwards SIGINT, SIGTERM, SIGHUP and SIGQUIT.

## Remove a vault

```sh
# a project (`ta list` prints its directory)
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

Decisions are ADRs in `docs/adr/`. The web UI is plain ES modules in `cmd/ta/web/`, embedded in the binary
with no build step; the header of `cmd/ta/web/app.js` maps its `lib/`, `components/` and `features/`.
