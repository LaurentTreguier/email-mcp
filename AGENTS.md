# AGENTS.md — email-mcp

## What this is

A Go **MCP server** that exposes any IMAP/SMTP mail account to LLM hosts over
**MCP Streamable HTTP**. The server is **stateless** and has a **single fixed
endpoint**:

```
POST https://<host>/mcp
```

Authentication is **OAuth 2.0 only** — no custom headers, no per-account
URL, no server-side configuration. The whole account setup is captured once
at OAuth consent time and travels encrypted inside the issued access token.
This matches MCP clients with named connections (Mistral Vibe, VS Code
`mcp.json` `"headers"`, Claude Desktop): each connection shares the same
`url` and differs only by its `Authorization` header.

- **Auth on `/mcp`**: `Authorization: ****** <access token>` — an access
  token from the built-in OAuth authorization server (`internal/oauthserver`);
  the account is embedded as an AES-256-GCM-encrypted claim, verified
  statelessly per request. Missing/invalid token → 401 +
  `WWW-Authenticate` challenge.
  - `/.well-known/oauth-protected-resource` (RFC 9728) →
    `/.well-known/oauth-authorization-server` (RFC 8414) advertises the OAuth
    flow to OAuth-aware clients.
- **OAuth endpoints**: `POST /oauth/register` (RFC 7591 DCR),
  `GET|POST /oauth/authorize` (HTML consent form — the single place account
  configuration is entered — with PKCE S256; the submitted account is
  validated with read-only checks via `verifyAccount`/`VerifyAccount`
  (internal/oauthserver/verify.go: DNS discovery + IMAP VerifyIMAP +
  SMTP VerifySMTP) and failures re-render the form with an error banner),
  `POST /oauth/token` (authorization_code + refresh_token; RS256 JWT access
  tokens, 24h TTL), `GET /.well-known/jwks.json`.
- **Consent-form fields** (unset IMAP/SMTP = DNS auto-discovery via SRV
  `_imaps/_imap/_submissions/_submission`, then MX, then
  `imap.<domain>`/`smtp.<domain>`): `email` + `password` (required),
  `username`, `display_name`, `auth_mechanism` (`plain` default, `login`,
  `xoauth2`), `sent_mailbox`, `drafts_mailbox`, and the IMAP/SMTP set
  `imap_host` / `imap_port` / `imap_tls` / `imap_starttls` / `imap_insecure` /
  `imap_skip_verify` plus the `smtp_*` twins (checkboxes). Parsed by
  `config.AccountFromFormValues`.
- `GET /` serves a JSON discovery doc; browsers (`Accept: text/html`) get a
  short HTML how-to page explaining the OAuth flow.
- Env/flags: `-addr` / `EMAIL_MCP_ADDR` (default `:8080`),
  `EMAIL_MCP_BASE_URL` (OAuth issuer; default derived per request),
  `EMAIL_MCP_TOKEN_SECRET` (32-byte secret encrypting the account claim in
  OAuth tokens; random per start if unset).

## Layout

```
cmd/email-mcp/main.go          entrypoint: flags, http.Server, graceful shutdown
internal/config/config.go      Account struct + AccountFromFormValues (consent-form parser)
internal/discovery/discovery.go DNS SRV/MX/well-known discovery + reachability + FillDefaults
internal/oauthserver/          built-in OAuth 2.0 authorization server (RFC 8414/7591,
                               PKCE auth-code + refresh, RS256 JWTs with encrypted
                               account claim, JWKS)
  verify.go                    consent-time account validation (VerifyAccount hook)
  templates/consent.html       consent form page (go:embed + template.ParseFS)
internal/mail/                 IMAP session + ops, MIME parsing, SMTP compose/send
  session.go                   lazy dial (TLS/STARTTLS/insecure), SASL, reconnect-once
  ops.go                       mailboxes, search, fetch, flags, move/copy/delete, append, quota, VerifyIMAP
  mime.go                      header parsing, text/html extraction, BODYSTRUCTURE walk
  send.go                      Compose (MIME multipart, threading) + Send (SMTP + APPEND Sent) + VerifySMTP
  util.go                      nowFunc, decodeBase64 helpers
internal/mcpserver/            MCP wiring per account (built per request)
  server.go                    New(acc): server (account arrives via the OAuth token)
  tools.go                     19 tools (see below)
  resources.go                 mail:// resources + URI templates
  prompts.go                   triage_inbox, summarize_thread, draft_reply, find_unanswered
  b64.go                       b64Encode, boolPtr helpers
internal/httpserver/           stateless HTTP routing: New() http.Handler
  httpserver.go                 POST /mcp → per-request MCP server (OAuth JWT only); / discovery; OAuth routes
  index.go                     GET / : JSON discovery doc + browser how-to page
  templates/howto.html         browser how-to page (go:embed + template.ParseFS)
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

- **Stateless, OAuth-only**: never add server-side account state and never
  put account data in the URL path/query — accounts are built per request in
  `httpserver.buildAccount` by verifying the `Authorization: ******` token with
  `oauthserver.VerifyAccessToken`. The only MCP endpoint is `POST /mcp`.
- **Secrets**: never in URLs, logs, or cache keys. Credentials are captured
  only on the OAuth consent form and travel solely inside the encrypted claim
  of the access token (AES-256-GCM with `EMAIL_MCP_TOKEN_SECRET`).
  `*_insecure`/`*_skip_verify` consent-form options are explicit opt-ins for
  local testing only (carry `//nolint:gosec` with a reason).
- Error paths: auth failures on `/mcp` must surface as HTTP 401 with a
  `WWW-Authenticate` OAuth challenge (see the `wrapped` handler in
  httpserver.go).
- Tests use in-memory fakes (`imapserver`, custom SMTP sink); DNS/dial hooks in
  `internal/discovery` are overridable vars for tests.
- **Commits**: conventional commit subject ≤72 chars lowercase, body wrapped
  ≤72 cols, trailer `Assisted-by: Copilot (Mistral Large 4)`. Branch: `develop`.
- CI (`.github/workflows/test.yml`) must stay green: gofmt/vet/tidy/build/test
  plus a docker build + container smoke test.
