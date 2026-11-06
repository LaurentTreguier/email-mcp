# email-mcp

A Go MCP server that exposes any IMAP/SMTP mail account to LLM hosts over **MCP Streamable HTTP**. The server is **stateless** and has a **single fixed endpoint** — the whole account setup travels in the client's request auth, which is how MCP clients with named connections (Mistral Vibe, VS Code `mcp.json`, Claude Desktop, ...) carry per-connection auth:

```
POST https://mcp.example.org/mcp
Authorization: ******   # see the three auth shapes below
```

Current release: **v0.1.0** (see [CHANGELOG.md](CHANGELOG.md)).

## Usage

The MCP endpoint URL is always the same; each named connection differs only by its auth:

```
https://mcp.example.org/mcp
```

Three auth shapes are accepted on `/mcp` (tried in order):

1. **OAuth bearer (recommended)** — email-mcp is its own OAuth 2.0 authorization server. Run the flow once (metadata at `/.well-known/oauth-authorization-server`, dynamic registration at `/oauth/register`, consent at `/oauth/authorize`, token at `/oauth/token`, JWKS at `/.well-known/jwks.json`); the issued access token is an RS256 JWT carrying the whole account (AES-256-GCM encrypted claim), so the client only stores **one bearer token** per connection. `/.well-known/oauth-protected-resource` (RFC 9728) advertises this to OAuth-aware clients; unauthenticated `/mcp` requests get a `401` + `WWW-Authenticate` challenge pointing at it.
2. **Single packed bearer** — for clients that accept only one auth header (e.g. Mistral Vibe): `Authorization: Bearer <base64url(JSON account)>` with `{"email":..., "password":..., "imap_host":..., ...}`.
3. **`X-Email-*` custom headers** — `X-Email-Address: user@gmail.com` (required) + `X-Email-Password: <app-password>` (or a raw `Authorization: Bearer` token used as an XOAUTH2 mail credential), plus optional overrides below.

The setup form at `/` (browsers) generates both the header config and the packed single-header bearer.

- **Address**: the full mail address, e.g. `user@gmail.com`. Its domain drives DNS auto-discovery.
- **Server settings are auto-discovered** from the address domain via DNS: `_imaps._tcp` / `_imap._tcp` and `_submissions._tcp` / `_submission._tcp` SRV records (RFC 6186), falling back to the MX host (RFC 7505) and then `imap.<domain>` / `smtp.<domain>` on the well-known ports.
- If no credential is present at all, the server falls back to MCP elicitation on the first tool call.

Optional header overrides (unset = auto-discovered via DNS):

| Header | Meaning |
|---|---|
| `X-Email-Username` | login name override (default: the full address) |
| `X-Email-Display-Name` | From-header display name |
| `X-Email-Auth-Mechanism` | `plain` (default with password header), `login`, `xoauth2` (default with Bearer) |
| `X-Email-IMAP-Host`, `X-Email-IMAP-Port`, `X-Email-IMAP-TLS: true`, `X-Email-IMAP-STARTTLS: true`, `X-Email-IMAP-Insecure: true`, `X-Email-IMAP-Skip-Verify: true` | IMAP endpoint override |
| `X-Email-SMTP-Host`, `X-Email-SMTP-Port`, `X-Email-SMTP-TLS: true`, `X-Email-SMTP-STARTTLS: true`, `X-Email-SMTP-Insecure: true`, `X-Email-SMTP-Skip-Verify: true` | SMTP endpoint override |
| `X-Email-Sent-Mailbox`, `X-Email-Drafts-Mailbox` | folder names (default `Sent` / `Drafts`) |

`GET /` returns a JSON discovery document describing the convention. **Browsers** (requests with `Accept: text/html`) instead get a plain HTML setup form at `/`: fill in your email address (and password for the packed-bearer variant) and the optional overrides, submit, and the page shows the fixed endpoint URL, a ready-to-copy client config with the corresponding `X-Email-*` headers (password stays an `${env:...}` placeholder there), and a single packed `Authorization: Bearer base64url(JSON)` value for single-header clients. There is also a link into the OAuth consent flow at `/oauth/authorize`.

## Features

**Reading (IMAP, RFC 9051/3501)**
- List/subscribe/create/rename/delete mailboxes, mailbox STATUS, quota
- UID search (from/to/cc/subject/body/text, dates, flags, size, UID sets)
- List message summaries; fetch full messages (headers, text + HTML bodies, attachment list)
- Download individual attachments (base64)

**Organizing**
- Set/add/remove flags (`\Seen`, `\Flagged`, `\Answered`, `\Deleted`, `\Draft`, keywords)
- Move / copy / delete (with optional permanent expunge) by UID or UID range

**Sending (SMTP submission, RFC 5321/6409)**
- `send_email`: compose MIME (text + HTML + base64 attachments, threading via
  `In-Reply-To`/`References`), submit over STARTTLS/implicit TLS with SASL auth
  (PLAIN / LOGIN / XOAUTH2), then APPEND a `\Seen` copy to the Sent mailbox
- `save_draft`: APPEND to Drafts with `\Draft`

**MCP surface**
- 19 tools (see `tools/list`), resources (`mail://mailboxes`, templates
  `mail://mailbox/{mailbox}`, `mail://message/{mailbox}/{uid}`,
  `mail://attachment/{mailbox}/{uid}/{part}`), and prompts
  (`triage_inbox`, `summarize_thread`, `draft_reply`, `find_unanswered`)

## Configuration

There is **no server-side account configuration** — the server is stateless. Per request, the account is built from the bearer token (OAuth JWT or packed base64 JSON) or the `X-Email-*` headers (table above); anything unset is auto-discovered via DNS, and a missing credential falls back to MCP elicitation.

Server-level settings are deployment knobs only:

| Env / flag | Meaning |
|---|---|
| `-addr` / `EMAIL_MCP_ADDR` | listen address (default `:8080`) |
| `EMAIL_MCP_BASE_URL` | public base URL used as the OAuth issuer (default: derived per request from scheme+host) |
| `EMAIL_MCP_TOKEN_SECRET` | 32-byte secret (base64 or raw) encrypting the account claim inside OAuth access tokens; random per start if unset (tokens don't survive restarts) |
| `EMAIL_MCP_AUTH_TOKEN` | optional endpoint gate: `Authorization: Bearer` must match it and is then *not* a mail credential |

### OAuth endpoints

`GET /.well-known/oauth-authorization-server` (RFC 8414) · `GET /.well-known/jwks.json` · `POST /oauth/register` (RFC 7591 DCR) · `GET|POST /oauth/authorize` (consent form; PKCE S256 required) · `POST /oauth/token` (`authorization_code` + `refresh_token` grants; access tokens are RS256 JWTs, 24h TTL, embedding the AES-encrypted account).

## Run

```sh
go build -o email-mcp ./cmd/email-mcp
./email-mcp -addr :8080
# stateless: POST https://mcp.example.org/mcp with X-Email-Address + credential headers
# behind a reverse proxy that terminates TLS:
# https://mcp.example.org  ->  http://127.0.0.1:8080
```

Flags/env: `-addr` / `EMAIL_MCP_ADDR` (default `:8080`); optional `EMAIL_MCP_AUTH_TOKEN` to gate the whole endpoint.

### Docker

```sh
docker build -t email-mcp .
docker run -p 8080:8080 email-mcp
# stateless: POST https://mcp.example.org/mcp with X-Email-* headers
# (put a TLS-terminating reverse proxy in front for HTTPS)
# optional env: -e EMAIL_MCP_AUTH_TOKEN=...
```

The image is a multi-stage build (golang:1.27-alpine → distroless static-debian13 nonroot, `EXPOSE 8080`, entrypoint `/email-mcp`).

## Client config (VS Code Copilot / Mistral Vibe named connection)

```json
{
  "mcpServers": {
    "gmail": {
      "type": "http",
      "url": "https://mcp.example.org/mcp",
      "headers": {
        "X-Email-Address": "user@gmail.com",
        "X-Email-Password": "${env:GMAIL_APP_PASSWORD}"
      }
    },
    "work": {
      "type": "http",
      "url": "https://mcp.example.org/mcp",
      "headers": {
        "Authorization": "Bearer ${env:EMAIL_MCP_WORK_TOKEN}"
      }
    }
  }
}
```

The `work` connection uses a token issued by the built-in OAuth flow (or a packed base64 JSON account); the `gmail` one uses raw `X-Email-*` headers. Multiple connections to different accounts are just multiple entries with the same `url` and different auth.

## Development

```sh
go test ./...   # unit tests against in-memory fake IMAP/SMTP servers + end-to-end MCP-over-HTTP tests
```

## Security notes

- Credentials are carried only by client request auth (bearer token or `X-Email-*` headers, or elicitation); the server keeps no account state and never logs them, and they never appear in URLs.
- OAuth access tokens are RS256 JWTs whose account claim is AES-256-GCM encrypted with `EMAIL_MCP_TOKEN_SECRET`; refresh tokens are random and rotated on use.
- TLS by default; the `*-Insecure` / `*-Skip-Verify` options are explicit opt-ins for local testing.
- The HTTP endpoint can be gated with a deployment-level bearer token (`EMAIL_MCP_AUTH_TOKEN`).
- Destructive tools are annotated (`delete_messages` has `destructiveHint`); keep a human in the loop in your client.
