---
status: accepted
---

# Humans move a vault as an export file sealed with a passphrase

A human moving to another Mac, keeping a backup, or seeding another vault needs every secret out at once:
one `ta reveal` per secret is slow, and a plaintext `.env` lands where agents can read it. `ta export FILE`
writes every environment of the vault in use to a new file whose names, values and update times are
sealed with AES-256-GCM under a key derived with PBKDF2-HMAC-SHA256 (600,000 iterations, a random 16-byte
salt) from a passphrase the person types twice on `/dev/tty`, at least 12 characters. `ta import FILE`
recognises an export file, asks for its passphrase on `/dev/tty`, checks the whole file, and merges it in
one locked write with the rule `.env` imports follow: existing secrets stay unless `-overwrite` is given.
Imported secrets keep the update times they were exported with. The export carries every value, so, as
`ta reveal` does, it first waits for the Mac's own prompt ([ADR 0005](0005-reveal-after-user-presence.md)),
and is, with it, an exception to [ADR 0001](0001-agents-use-secrets-by-name.md)'s rule that code reads a
value only for `ta run`. An agent can neither answer that prompt nor type a passphrase on a terminal it
does not have; without one, `ta export` fails before any prompt appears. The file never holds the vault's
Keychain key: it opens on any Mac with the passphrase alone.

## Considered Options

- Copying the vault file with its Keychain key: rejected; the key would leave the Keychain in a form
  anyone holding the copy can use, and a vault's key is filed under its directory, so a project vault
  opens only in a repository at the same path.
- Argon2id or scrypt from `golang.org/x/crypto`: memory-hard, so guessing a passphrase on a GPU costs
  more, but `ta` depends on nothing outside the Go standard library, which has PBKDF2 since Go 1.24.
  The file records its KDF and iteration count, so a later `ta` can switch or raise them and still read
  version 1 files.
- Writing to stdout: rejected; agent harnesses and logs capture stdout, and a captured file can be
  guessed at offline for as long as anyone keeps it. The export goes only to a new file, mode 0600.
- Taking the passphrase from a flag, an environment variable or stdin: rejected; other processes can
  read arguments and environments, and an agent could supply any of them.
- Exporting a single environment: left out; the whole vault is the unit of a move or a backup. `-e` on
  an export file's import is an error rather than a filter, so nobody mistakes it for a target.
- Export and export-file import in the web UI: left out; the passphrase would travel over HTTP to a
  server every local process can reach ([ADR 0003](0003-web-ui-loopback-write-only.md)).

## Rails

Applies to: `cmd/ta/**`

- `ta export` seals the vault only in `exportVault`, after `confirmUser` has approved; a refused, cancelled or unavailable prompt writes nothing. `check:rails.export-asks-first`
- An export file holds only `format`, `version`, `kdf`, `iterations`, `salt`, `nonce` and `ciphertext`; names, values and update times exist in it only inside AES-256-GCM ciphertext under a PBKDF2-HMAC-SHA256 key of the passphrase (at least 600,000 iterations), with a fresh random salt and nonce on every export. It never holds the vault's Keychain key. `check:rails.export-sealed`
- `ta import` reads every export version `ta` has written; any change to an export file's encrypted plaintext, which is the vault's version 2 layout today, bumps `exportVersion`. `check:rails.export-format-compat`
- A passphrase is read only with `readHidden` on the terminal that `openTerminal` opens from `/dev/tty`; never from an argument, a flag, an environment variable or stdin. `ta export` asks for it twice and refuses fewer than 12 characters (Unicode code points). `check:rails.export-passphrase-tty`
- `ta export` writes only to a new file it creates with mode 0600; it never replaces an existing file and never writes to stdout. `check:rails.export-new-file`
- `ta import` decrypts and checks an entire export file (passphrase, version, key derivation settings, every environment, name and value) before one locked write; anything wrong imports nothing, and its errors name no secret. `check:rails.import-export-validated`
