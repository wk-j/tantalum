---
name: tantalum
description: Use when a command needs an API key, token, password, or other credential. Run it through `ta`, the tantalum secret manager, so the value never enters your context.
---
<!-- Managed by "ta skill setup"; "ta skill remove" removes it. -->

Run `ta skill show` and follow the guide it prints. The guide comes from the installed `ta`, so it
describes the commands of that version.

Until you have read it, use a secret only by name, through `ta run -s NAME -- COMMAND`: never read
`.env` or key files for a value, and never ask the human to paste one. If `ta skill show` fails, the
installed `ta` predates the guide; run `ta help`, which ends with a summary of the rules for agents.
