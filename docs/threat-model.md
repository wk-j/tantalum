# Threat model

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
