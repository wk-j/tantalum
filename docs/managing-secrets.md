# Managing secrets

Commands for the human who owns the vault. Agents use secrets through `ta run`; see
[agents.md](agents.md).

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

`ta help` prints every command and flag. Which vault a command uses depends on your working directory; see
[how-it-works.md](how-it-works.md).

## Import a .env file

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

## Move a vault: export and import

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

## Remove a vault

```sh
# a project (`ta list` prints its directory)
rm -rf ~/.tantalum/projects/api-1a2b3c4d5e6f
security delete-generic-password -s tantalum -a "$HOME/.tantalum/projects/api-1a2b3c4d5e6f"

# the default vault (project vaults stay)
rm -f ~/.tantalum/vault.json ~/.tantalum/vault.lock
security delete-generic-password -s tantalum -a "$HOME/.tantalum"
```

Uninstalling `ta` (`brew uninstall tantalum`) removes neither: vaults and their Keychain keys stay until you
delete them as above.
