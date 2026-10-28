---
status: accepted
---

# Environments are separate namespaces with no fallback

Secrets are grouped into named environments such as `dev` and `prod`, so one name can hold a different value
in each: `-e ENV` on `set`, `rm` and `run` (default `default`), `ta list` shows every environment, the web UI
has one tab per environment and the API is `/api/secrets/{env}/{name}`. A secret missing from the selected
environment is an error and is never taken from `default` or another environment: storing a shared token in
each environment costs less than an agent silently running against another environment's value. The vault
format became version 2; version 1 vaults still load, as environment `default`, and are rewritten as
version 2 on the next change, and a `ta` that does not know a version stops instead of overwriting the file.

Recorded in the Krypton timeline as `tl-20261028T042335Z-2bf3fa`.

## Rails

Applies to: `cmd/ta/**`

- `ta run` looks secrets up only in the environment chosen by `-e` (default `default`); it never falls back to `default` or another environment, and a name missing there fails the command. `check:rails.env-no-fallback`
- An environment exists only while it holds at least one secret: removing its last secret removes the environment. `check:rails.env-lifecycle`
- `ta` loads every vault format it has written, version 1 (one flat set of secrets, loaded as environment `default`) and version 2, and every write produces the current version. `check:rails.vault-format-compat`
- The vault format version is authenticated as AEAD additional data, and a vault whose version `ta` does not know is an error that no command overwrites. `check:rails.vault-unknown-version`
- Any change to the encrypted plaintext layout bumps `vaultVersion` and keeps every earlier version loading.
