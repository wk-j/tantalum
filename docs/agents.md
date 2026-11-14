# Agents

An agent uses a secret by name through `ta run`; it never sees the value.

```sh
ta run -s GITHUB_TOKEN -- gh api user
ta run -e prod -s DB_URL -- sh -c 'psql "$DB_URL" -c "select 1"'
ta run -s GH_TOKEN=GITHUB_TOKEN_WORK -- gh repo list    # VAR=NAME renames the variable
```

`run` passes secrets to the command as environment variables and replaces every vault value in its
stdout/stderr with `[REDACTED:ENV/NAME]`. The variables exist only in the child, so reference them
inside single-quoted `sh -c '...'`.

## Give your agent the skill

The rules an agent follows are in [`skills/tantalum/SKILL.md`](../skills/tantalum/SKILL.md). Link the
skill into your agent's skills directory.

Installed with Homebrew, the skill is in the formula's share directory:

```sh
ln -s "$(brew --prefix tantalum)/share/tantalum/skills/tantalum" ~/.agents/skills/tantalum   # omp
ln -s "$(brew --prefix tantalum)/share/tantalum/skills/tantalum" ~/.claude/skills/tantalum   # Claude Code
```

Installed from a clone, run this in the clone:

```sh
ln -s "$PWD/skills/tantalum" ~/.agents/skills/tantalum     # omp
ln -s "$PWD/skills/tantalum" ~/.claude/skills/tantalum     # Claude Code
```

Agents without skill support: paste its Rules section into `AGENTS.md` / `CLAUDE.md`.

## Exit status

`ta`'s own errors exit `125`; `run` exits with the command's status (`126` not executable,
`127` not found, `128+N` killed by signal N) and forwards SIGINT, SIGTERM, SIGHUP and SIGQUIT.

What redaction covers, and what it cannot stop, is in [threat-model.md](threat-model.md).
