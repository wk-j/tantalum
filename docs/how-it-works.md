# How it works

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

The reasons behind these choices are recorded as ADRs in [adr/](adr/).
