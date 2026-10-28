---
status: accepted
---

# Humans reveal a value only after the Mac confirms they are present

A human sometimes needs a stored value back, to paste it into a dashboard or check it, and `ta run -- printenv`
only shows `[REDACTED:…]`. `ta reveal [-e ENV] [-c] NAME` shows one value after the system's LocalAuthentication
prompt approves: Touch ID, or an Apple Watch or the login password on a Mac without a Touch ID sensor (policy
`LAPolicyDeviceOwnerAuthentication`). The prompt names the secret and where it goes, so the person sees what they
approve. The value goes to `/dev/tty`, or with `-c` to the clipboard, never to stdout or stderr, which agent
harnesses and logs capture. An agent cannot answer the prompt, so this keeps the goal of
[ADR 0001](0001-agents-use-secrets-by-name.md) and is the one exception to its rule that code reads a value only
for `ta run`. Talking to LocalAuthentication and the pasteboard needs cgo, so building `ta` needs the Xcode
command line tools.

Recorded in the Krypton timeline as `tl-20261028T110414Z-3993f3`.

## Considered Options

- A reveal button in the web UI: rejected, as in [ADR 0003](0003-web-ui-loopback-write-only.md); any local process
  holding the page token could call it, and the page cannot ask the Mac whether a person is present.
- Touch ID only (`LAPolicyDeviceOwnerAuthenticationWithBiometrics`): rejected; a Mac without a sensor or paired
  Touch ID keyboard, such as a Mac Studio, could never reveal anything.
- Printing to stdout: rejected; an agent that starts `ta reveal` would get the value in its context the moment a
  person approves a prompt they may not connect to the agent.
- `pbcopy` for the clipboard: rejected; it cannot mark the value concealed, so clipboard managers would keep it in
  their history.

## Rails

Applies to: `cmd/ta/**`

- `ta reveal` reads a value only after `confirmUser`, initialised to the system prompt `confirmUserPresence`, has approved; a refused, cancelled or unavailable prompt reveals nothing. `check:rails.reveal-asks-first`
- `ta reveal` hands the value only to a file opened from `/dev/tty` or to the clipboard; never to stdout, stderr, a log or an HTTP response. `check:rails.reveal-sinks`
- A value copied with `-c` is cleared from the clipboard 45 seconds later, unless something else was copied in between, which is left alone. `check:rails.reveal-clipboard-clears`
- A value copied with `-c` carries the `org.nspasteboard.ConcealedType` marker, so clipboard managers that honour it keep no history of it.
- Without `-c` and without a terminal, `ta reveal` fails before any prompt appears, so nobody is asked to approve a reveal that nobody can see.
