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

The skill teaches an agent the rules for using secrets through `ta`:

```sh
ta skill setup     # into every agent found: Claude Code, Codex, OpenCode, Pi, omp
ta skill status    # per agent: found or not, and its skill current, outdated, missing or foreign
ta skill remove    # take it out of every agent again
ta skill show      # print the guide the skill gives agents
```

`setup` writes a short `SKILL.md` into each agent's skills directory: `~/.claude/skills/tantalum` for
Claude Code, and `~/.agents/skills/tantalum`, shared by the others. With no agent found, it writes only
the shared one. The file asks the agent to run `ta skill show`, which prints the guide built into the
installed `ta`, so upgrading `ta` also updates what agents are told.

Running `setup` again is safe. It updates `outdated` skills, which include copies and symlinks of the
skill made by hand before this command existed; a symlink is replaced by a directory, and what it
pointed to is left as it was. Something else installed under the name `tantalum` is left alone unless
you pass `-force`. `-agent AGENT` (repeatable: `claude`, `codex`, `opencode`, `pi`, `omp`) limits
`setup` or `remove` to those agents, and `-dry-run` shows what would change.

Agents without skill support: paste the Rules section of `ta skill show` into `AGENTS.md` / `CLAUDE.md`.

## Exit status

`ta`'s own errors exit `125`; `run` exits with the command's status (`126` not executable,
`127` not found, `128+N` killed by signal N) and forwards SIGINT, SIGTERM, SIGHUP and SIGQUIT.

What redaction covers, and what it cannot stop, is in [threat-model.md](threat-model.md).
