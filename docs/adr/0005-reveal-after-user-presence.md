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
[ADR 0001](0001-agents-use-secrets-by-name.md) and is, with the encrypted export of
[ADR 0006](0006-export-file-sealed-with-a-passphrase.md), an exception to its rule that code reads a value only
for `ta run`. Talking to LocalAuthentication and the pasteboard needs cgo, so building `ta` needs the Xcode
command line tools.

The web UI reveals the same way. A cell's **Copy value** button calls `POST /api/secrets/{env}/{name}/reveal`,
and `ta serve`, running on the Mac, raises the same prompt and copies the value to the clipboard there; the
response carries no value ([ADR 0003](0003-web-ui-loopback-write-only.md)). A process holding the page token
gains nothing by calling it: it could raise the same prompt by running `ta reveal -c`. Both paths go through
one function, `revealSecret`.

Recorded in the Krypton timeline as `tl-20261028T110414Z-3993f3`; the web UI's reveal as `tl-20261029T022946Z-1f1e40`.

## Considered Options

- Showing the value in the web page: rejected, as in [ADR 0003](0003-web-ui-loopback-write-only.md); the value
  would travel in an HTTP response, which any local process holding the page token could request too.
- Touch ID only (`LAPolicyDeviceOwnerAuthenticationWithBiometrics`): rejected; a Mac without a sensor or paired
  Touch ID keyboard, such as a Mac Studio, could never reveal anything.
- Printing to stdout: rejected; an agent that starts `ta reveal` would get the value in its context the moment a
  person approves a prompt they may not connect to the agent.
- `pbcopy` for the clipboard: rejected; it cannot mark the value concealed, so clipboard managers would keep it in
  their history.

## Rails

Applies to: `cmd/ta/**`

- `ta reveal` and the web UI read a value only in `revealSecret`, after `confirmUser`, initialised to the system prompt `confirmUserPresence`, has approved; a refused, cancelled or unavailable prompt reveals nothing. `check:rails.reveal-asks-first`
- `revealSecret` hands the value only to a file opened from `/dev/tty` or to the clipboard; never to stdout, stderr, a log or an HTTP response. `check:rails.reveal-sinks`
- A value copied to the clipboard, by `ta reveal -c` or the web UI, is cleared 45 seconds later, unless something else was copied in between, which is left alone. `check:rails.reveal-clipboard-clears`
- A copied value carries the `org.nspasteboard.ConcealedType` marker, so clipboard managers that honour it keep no history of it.
- Without `-c` and without a terminal, `ta reveal` fails before any prompt appears, so nobody is asked to approve a reveal that nobody can see.
