---
status: accepted
---

# Agents use secrets by name and never see values

Everything an agent reads (`.env` files, command output, text pasted into chat) lands in its context, the
model provider's logs and session transcripts. tantalum is therefore a CLI that any agent able to run a
shell can use: secrets live in an AES-256-GCM vault whose secret names are encrypted too, its random 256-bit
key sits in the macOS login Keychain so nobody types a password, and an agent uses a secret by name through
`ta run`, which hands the value only to the child process's environment and redacts every vault value from
the child's stdout and stderr. This guards against accidental leaks only: an agent running as the same user
can still transform a value before printing it or read the key with `security find-generic-password`;
containing a deliberately hostile agent needs a separate OS user or a sandbox.

Recorded in the Krypton timeline as `tl-20261028T035903Z-be5ccf`.

## Considered Options

- Protect against a deliberately hostile agent (broker running as another OS user, sandbox): rejected for
  the first version; it costs every user a separate account or VM, while the goal is keeping values out of
  context, logs and transcripts.

## Rails

Applies to: `cmd/ta/**`

- Code reads a secret's value only to build the `ta run` child's environment and the output redactor, and to reveal it (`ta reveal` or the web UI's **Copy value**) once the person at the Mac has approved ([ADR 0005](0005-reveal-after-user-presence.md)); no stdout or stderr output, log line, error message or HTTP response contains a value, and only the vault reader and writer serialize types that hold values (`secret`, `environments`). `check:rails.value-sinks`
- `ta run` sends the child's stdout and stderr through redactors built from every environment in the vault, not only the injected secrets, and each match becomes `[REDACTED:ENV/NAME]`. When ta runs in a terminal, the child gets a pseudo-terminal of its own (stdin, the streams that go to ta's terminal, and its `/dev/tty`) whose output only that redactor reads; a stream redirected away from the terminal goes through a pipe into its redactor. `check:rails.run-redacts-all`
- The vault file holds only `version`, `nonce` and `ciphertext`; secret names, values and update times exist on disk only inside the AES-256-GCM ciphertext, sealed under a 256-bit key with a fresh random nonce on every write. `check:rails.vault-encrypted`
- Values enter the vault only through `ta set` (hidden prompt or stdin, never a command-line argument or flag) or the web UI's JSON request body. `check:rails.value-not-from-argv`
- `ta` hands the vault key to `/usr/bin/security` on stdin (`security -i`), never in a process argument that any local process could read with `ps`. `check:rails.key-not-in-argv`
- The vault key exists only in the login Keychain (service `tantalum`, account = vault directory) and in `ta`'s memory; `ta` never writes it to a file, an environment variable or any output.
