---
status: accepted
---

# The built-in web UI is loopback-only and write-only for values

Humans manage secrets in a browser through `ta serve`, an HTTP server built into `ta` (Go `net/http`, page
embedded in the binary). Every local process, agents included, can reach localhost, so an endpoint that
returned a value would undo [ADR 0001](0001-agents-use-secrets-by-name.md): the API lists environments, names
and update times, stores and deletes secrets, and never returns a value. The server listens on loopback only,
creates a new random token per run that reaches the browser in the URL fragment, and rejects requests whose
`Host` or `Origin` is not the server itself, which blocks other websites and DNS rebinding. A leaked token
lets its holder add, replace or delete secrets, but not read one.

Recorded in the Krypton timeline as `tl-20261028T041258Z-379c11`.

## Considered Options

- A "reveal" button for humans: rejected; any local process holding the token could call the same endpoint.
  A forgotten value is fetched again from its source and replaced.

## Rails

Applies to: `cmd/ta/**`

- `ta serve` listens only on loopback addresses and refuses any other `-addr` before it opens the vault. `check:rails.serve-loopback`
- No HTTP response (API, static file, error) contains a secret value; no endpoint returns one. `check:rails.api-write-only`
- Every route answers only requests whose `Host` is this server and whose `Origin`, when present, is this server over `http://`; every `/api/` route also requires the run's bearer token. `check:rails.serve-guard`
- Every response to a request that passes the `Host` and `Origin` checks carries a Content-Security-Policy that allows only same-origin resources, no inline or eval'd script and no framing. `check:rails.serve-csp`
- The access token is 256 bits from `crypto/rand`, new on every run, and reaches the browser only in the URL fragment (`#token=`), which the page moves into `sessionStorage` and removes from the address bar; it never travels in a query string, a cookie or a file.
