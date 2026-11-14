---
status: accepted
---

# The command is `ta`; stored names keep `tantalum`

The command was renamed from `tantalum` to `ta`, tantalum's chemical symbol, because the full name was too
long to type in every `run`; `ta` is the shortest name that still points at the project and clashed with
no command, alias or function on the machine. It is built from `cmd/ta` and installed with
`go install ./cmd/ta`. Names nobody types often keep `tantalum`, so existing vaults and Keychain keys stay
valid and stay recognizable: the project name, `~/.tantalum`, `TANTALUM_HOME` and the Keychain service
`tantalum`.

Recorded in the Krypton timeline as `tl-20261028T040257Z-acdaa0`.

## Rails

Applies to: `cmd/ta/**`, `README.md`, `docs/*.md`, `skills/**`

- Usage text, messages, the web UI, `README.md`, the guides in `docs/` and `skills/**` invoke the command as `ta` (`ta run`, `ta set`, …), never as `tantalum <subcommand>`. `check:rails.command-name`
- Errors from `ta` itself are written to stderr prefixed `ta: `. `check:rails.error-prefix`
- Names stored on disk or in the Keychain keep `tantalum`: the default vault directory `~/.tantalum`, the override variable `TANTALUM_HOME` and the Keychain service `tantalum`; renaming any of them orphans existing vaults and keys. `check:rails.persistent-names`
