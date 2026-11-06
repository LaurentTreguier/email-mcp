# AGENTS.md — email-mcp

## What this is

A Go **MCP server** that exposes any IMAP/SMTP mail account to LLM hosts over
**MCP Streamable HTTP**. The server is **stateless** and has a **single fixed
endpoint**:

```
POST https://<host>/mcp
```

The whole account setup travels in the client's **`X-Email-*` request
headers** — no per-account URL, no server-side configuration. This matches
MCP clients with named connections (Mistral Vibe, VS Code `mcp.json`
`"headers"`, Claude Desktop): each connection shares the same `url` and
differs only by its headers.

- **Auth on `/mcp`** (tried in order, all setup-time, never in the URL):
  1. `Authorization: Bearer <JWT>` — access token from the built-in OAuth
     authorization server (`internal/oauthserver`); the account is embedded
     as an AES-256-GCM-encrypted claim, verified statelessly
  2. `Authorization: Bearer <base64url(JSON account)>` — single-header
     packing of the whole account (clients with one auth header, e.g.
     Mistral Vibe)
  3. `X-Email-Address: user@gmail.com` + `X-Email-Password`, or a raw
     `Authorization: Bearer` token used as the mail credential (XOAUTH2)
  - `/.well-known/oauth-protected-resource` (RFC 9728) →
    `/.well-known/oauth-authorization-server` (RFC 8414) advertises the OAuth
    flow; unauthenticated `/mcp` gets 401 + `WWW-Authenticate` challenge.
  - Fallback: MCP elicitation on the first `tools/call`.
- **OAuth endpoints**: `POST /oauth/register` (RFC 7591 DCR),
  `GET|POST /oauth/authorize` (HTML consent form, PKCE S256),
  `POST /oauth/token` (authorization_code + refresh_token; RS256 JWT access
  tokens, 24h TTL), `GET /.well-known/jwks.json`.
- **Optional overrides** (unset = DNS auto-discovery via SRV
  `_imaps/_imap/_submissions/_submission`, then MX, then
  `imap.<domain>`/`smtp.<domain>`): `X-Email-Username`,
  `X-Email-Display-Name`, `X-Email-Auth-Mechanism` (`plain` default with
  password header, `login`, `xoauth2`), `X-Email-Sent-Mailbox`,
  `X-Email-Drafts-Mailbox`, and the IMAP/SMTP set `X-Email-IMAP-Host` /
  `-Port` / `-TLS` / `-STARTTLS` / `-Insecure` / `-Skip-Verify` plus the
  `X-Email-SMTP-*` twins (booleans accept `1`/`true`/`yes`).
- `GET /` serves a JSON discovery doc; browsers (`Accept: text/html`) get a
  plain HTML setup form that emits the fixed endpoint URL + a copy-pasteable
  client config with the `X-Email-*` headers (password stays a
  `${env:...}` placeholder).
- Optional deployment-level gate: `EMAIL_MCP_AUTH_TOKEN` env (then `Bearer`
  must match it and is NOT a mail credential). Other env/flags:
  `-addr` / `EMAIL_MCP_ADDR` (default `:8080`), `EMAIL_MCP_BASE_URL`
  (OAuth issuer; default derived per request), `EMAIL_MCP_TOKEN_SECRET`
  (32-byte secret encrypting the account claim in OAuth tokens; random per
  start if unset).

## Layout

```
cmd/email-mcp/main.go          entrypoint: flags, http.Server, graceful shutdown
internal/config/config.go      per-request Account struct + FromHeaders + bearer pack/unpack
internal/discovery/discovery.go DNS SRV/MX/well-known discovery + reachability
internal/oauthserver/          built-in OAuth 2.0 authorization server (RFC 8414/7591,
                               PKCE auth-code + refresh, RS256 JWTs with encrypted
                               account claim, JWKS)
internal/mail/                 IMAP session + ops, MIME parsing, SMTP compose/send
  session.go                   lazy dial (TLS/STARTTLS/insecure), SASL, reconnect-once
  ops.go                       mailboxes, search, fetch, flags, move/copy/delete, append, quota
  mime.go                      header parsing, text/html extraction, BODYSTRUCTURE walk
  send.go                      Compose (MIME multipart, threading) + Send (SMTP + APPEND Sent)
  util.go                      nowFunc, decodeBase64 helpers
internal/mcpserver/            MCP wiring per account (built per request)
  server.go                    New(acc): server + elicitation middleware
  tools.go                     19 tools (see below)
  resources.go                 mail:// resources + URI templates
  prompts.go                   triage_inbox, summarize_thread, draft_reply, find_unanswered
  credentials.go               ensureCredentials: header password, else elicitation
  b64.go                       b64Encode, boolPtr helpers
internal/httpserver/           stateless HTTP routing: New() http.Handler
  httpserver.go                 POST /mcp → per-request MCP server (auth: OAuth JWT /
                               packed bearer / X-Email-* headers); / discovery; OAuth routes
  form.go                      HTML setup form + result page at / (header config +
                               packed single-header bearer)
  *_test.go, fakeimap_test.go  e2e tests over httptest + in-memory IMAP + full OAuth flow
Dockerfile                     multi-stage: golang:1.27-alpine → distroless static-debian13:nonroot
.github/workflows/test.yml     CI: go mod tidy check, verify, gofmt, build, vet, test + docker build/smoke
.github/dependabot.yml         weekly gomod/docker/actions updates
```

### MCP surface

- **19 tools**: `account_info`, `list_mailboxes`, `create_mailbox`,
  `delete_mailbox`, `rename_mailbox`, `subscribe_mailbox`, `unsubscribe_mailbox`,
  `mailbox_status`, `search_messages`, `list_messages`, `get_message`,
  `get_attachment`, `set_flags`, `move_messages`, `copy_messages`,
  `delete_messages` (destructive hint), `send_email`, `save_draft`, `get_quota`.
- **Resources**: `mail://mailboxes`; templates `mail://mailbox/{mailbox}`,
  `mail://message/{mailbox}/{uid}`, `mail://attachment/{mailbox}/{uid}/{part}`.
- **Prompts**: `triage_inbox`, `summarize_thread`, `draft_reply`, `find_unanswered`.

## Dependencies

Go 1.27.3 (`go.mod`). Direct deps (keep at latest with `go get -u ./...` +
`go mod tidy`):

- `github.com/modelcontextprotocol/go-sdk` — MCP server/client, Streamable HTTP
- `github.com/emersion/go-imap/v2` (+ `imapclient`) — IMAP4rev2 client
- `github.com/emersion/go-smtp` — SMTP submission client
- `github.com/emersion/go-message` — MIME build/parse
- `github.com/emersion/go-sasl` — SASL PLAIN/LOGIN/XOAUTH2
- `github.com/golang-jwt/jwt/v5` — RS256 signing/verification for OAuth tokens

## Commands

```sh
go build ./...          # build
go vet ./...            # vet
gofmt -l .              # must print nothing (run gofmt -w . to fix)
go test -count=1 ./...  # tests: fake in-memory IMAP (imapserver) + SMTP sink + e2e MCP-over-HTTP
go mod tidy && git diff --exit-code go.mod go.sum   # CI checks tidiness
docker build -t email-mcp . && docker run -p 8080:8080 email-mcp
go run ./cmd/email-mcp -addr :8080   # local run; try GET / with Accept: text/html
```

## Conventions

- **Stateless, auth-driven**: never add server-side account state and never
  put account data in the URL path/query — accounts are built per request in
  `httpserver.buildAccount` from the bearer token or `X-Email-*` headers
  (`config.FromHeaders` / `config.DecodeBearerAccount` /
  `oauthserver.VerifyAccessToken`). The only MCP endpoint is `POST /mcp`.
- **Secrets**: never in URLs, logs, or cache keys. Credentials travel only in
  the `Authorization` header (OAuth JWT, packed base64 JSON, or raw token) or
  `X-Email-Password`; elicitation fallback asks via a `writeOnly` schema
  field. OAuth account claims are AES-256-GCM encrypted with
  `EMAIL_MCP_TOKEN_SECRET`. `*-Insecure`/`*-Skip-Verify` options are explicit
  opt-ins for local testing only (carry `//nolint:gosec` with a reason).
- Error paths: account-build failures on `/mcp` must surface as HTTP 401
  (with a `WWW-Authenticate` OAuth challenge) when no credential was given,
  or HTTP 400 with a descriptive message when the account is malformed (see
  the `wrapped` handler in httpserver.go).
- Tests use in-memory fakes (`imapserver`, custom SMTP sink); DNS/dial hooks in
  `internal/discovery` are overridable vars for tests.
- **Commits**: conventional commit subject ≤72 chars lowercase, body wrapped
  ≤72 cols, trailer `Assisted-by: Copilot (Mistral Large 4)`. Branch: `develop`.
- CI (`.github/workflows/test.yml`) must stay green: gofmt/vet/tidy/build/test
  plus a docker build + container smoke test.
