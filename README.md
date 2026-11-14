# tantalum

Local secret manager for AI coding agents on macOS. The command is `ta`.

Anything an agent reads — `.env` files, command output, pasted text — lands in its context, the
provider's logs and session transcripts. With `ta`, an agent uses a secret by **name** and never sees
its **value**.

## Install

```sh
brew install wk-j/tap/tantalum
```

Or from source, with Go and the Xcode command line tools (cgo for Touch ID and the clipboard):

```sh
git clone https://github.com/wk-j/tantalum.git && cd tantalum
go install ./cmd/ta
```

## Quick start

In your own terminal:

```sh
ta init                  # once per vault: creates it and its Keychain key
ta set GITHUB_TOKEN      # hidden prompt; the value is never an argument
ta serve                 # or manage secrets in a web UI; open the printed URL
```

What the agent runs:

```sh
ta run -s GITHUB_TOKEN -- gh api user   # the secret is a variable of gh only; its value is redacted from the output
```

Then [give your agent the skill](docs/agents.md#give-your-agent-the-skill), so it uses secrets this way
on its own.

## Documentation

- [Managing secrets](docs/managing-secrets.md): every command, importing `.env` files, moving and removing a vault
- [Agents](docs/agents.md): `ta run`, the agent skill, exit status
- [How it works](docs/how-it-works.md): vaults, projects, environments, the web UI
- [Threat model](docs/threat-model.md): what `ta` protects against, and what it does not
- [Development](docs/development.md): tests, code layout, releasing to Homebrew
- [Decisions](docs/adr/): ADRs
