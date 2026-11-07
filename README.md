# email-mcp

A Go MCP server that exposes any IMAP/SMTP mail account to LLM hosts over **MCP Streamable HTTP**. The server is **stateless** and has a **single fixed endpoint** — authentication is **OAuth 2.0 only**: the client sends an access token issued by the built-in authorization server, and the whole account setup (captured once at consent time) travels encrypted inside that token. This is how MCP clients with named connections (Mistral Vibe, VS Code `mcp.json`, Claude Desktop, ...) carry per-connection auth:

```
POST https://mcp.example.org/mcp
Authorization: ****** <access token>
```

Current release: **v0.2.1** (see [CHANGELOG.md](CHANGELOG.md)).

## Usage

The MCP endpoint URL is always the same; each named connection differs only by its OAuth access token:

```
https://mcp.example.org/mcp
```

**Authentication is OAuth 2.0 only.** email-mcp is its own OAuth 2.0 authorization server; run the flow once and store the issued token:

1. Dynamic client registration (RFC 7591): `POST /oauth/register` with `{"redirect_uris":["..."]}` → `client_id` / `client_secret`.
2. Authorization + consent: `GET /oauth/authorize?client_id=...&redirect_uri=...&state=...&scope=email&code_challenge=...&code_challenge_method=S256` (PKCE S256 required). The **consent form** is where the mail account is configured: email + password/app password (required), plus optional username, display name, auth mechanism, IMAP/SMTP host/port/TLS/STARTTLS/insecure/skip-verify overrides, and Sent/Drafts folder names. Empty IMAP/SMTP settings are auto-discovered via DNS. On submit the account is **validated with read-only checks** (IMAP login + `LIST` + `STATUS INBOX`; SMTP `EHLO`+`AUTH`+`NOOP`; nothing is sent) — if anything fails, the form is re-shown with the exact error and no authorization is granted.
3. Token exchange: `POST /oauth/token` (`authorization_code` grant + PKCE verifier) → access token (RS256 JWT, 24h, carrying the whole account as an AES-256-GCM encrypted claim) + rotating refresh token (`refresh_token` grant).
4. Use `Authorization: ****** <access token>` on `POST /mcp`.

Discovery for OAuth-aware clients: `/.well-known/oauth-protected-resource` (RFC 9728) → `/.well-known/oauth-authorization-server` (RFC 8414); JWKS at `/.well-known/jwks.json`. Unauthenticated or invalid-token `/mcp` requests get `401` + a `WWW-Authenticate` challenge pointing at the resource metadata.

- **Address**: the full mail address entered at consent, e.g. `user@gmail.com`. Its domain drives DNS auto-discovery when no explicit servers are configured.
- **Server settings are auto-discovered** from the address domain via DNS: `_imaps._tcp` / `_imap._tcp` and `_submissions._tcp` / `_submission._tcp` SRV records (RFC 6186), falling back to the MX host (RFC 7505) and then `imap.<domain>` / `smtp.<domain>` on the well-known ports.

`GET /` returns a JSON discovery document; **browsers** (`Accept: text/html`) get a short HTML page at `/` explaining the OAuth flow and linking the endpoints.

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

There is **no server-side account configuration** — the server is stateless. The account is captured once, at OAuth consent time, and travels encrypted inside the access token; anything left unset on the consent form (IMAP/SMTP servers) is auto-discovered via DNS per request.

Server-level settings are deployment knobs only:

| Env / flag | Meaning |
|---|---|
| `-addr` / `EMAIL_MCP_ADDR` | listen address (default `:8080`) |
| `EMAIL_MCP_BASE_URL` | public base URL used as the OAuth issuer (default: derived per request from scheme+host) |
| `EMAIL_MCP_TOKEN_SECRET` | 32-byte secret (base64 or raw) encrypting the account claim inside OAuth access tokens; random per start if unset (tokens don't survive restarts) |

### OAuth endpoints

`GET /.well-known/oauth-protected-resource` (RFC 9728) · `GET /.well-known/oauth-authorization-server` (RFC 8414) · `GET /.well-known/jwks.json` · `POST /oauth/register` (RFC 7591 DCR) · `GET|POST /oauth/authorize` (consent form; PKCE S256 required) · `POST /oauth/token` (`authorization_code` + `refresh_token` grants; access tokens are RS256 JWTs, 24h TTL, embedding the AES-encrypted account).

## Run

```sh
go build -o email-mcp ./cmd/email-mcp
./email-mcp -addr :8080
# stateless: POST https://mcp.example.org/mcp with Authorization: ****** <OAuth access token>
# behind a reverse proxy that terminates TLS:
# https://mcp.example.org  ->  http://127.0.0.1:8080
```

Flags/env: `-addr` / `EMAIL_MCP_ADDR` (default `:8080`); `EMAIL_MCP_BASE_URL` and `EMAIL_MCP_TOKEN_SECRET` for the OAuth issuer and token encryption.

### Docker

```sh
docker build -t email-mcp .
docker run -p 8080:8080 email-mcp
# stateless: POST https://mcp.example.org/mcp with Authorization: ****** <OAuth access token>
# (put a TLS-terminating reverse proxy in front for HTTPS)
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
        "Authorization": "******"
      }
    },
    "work": {
      "type": "http",
      "url": "https://mcp.example.org/mcp",
      "headers": {
        "Authorization": "******"
      }
    }
  }
}
```

Each connection uses an access token issued by the built-in OAuth flow (run register → authorize/consent → token once per account; refresh via the `refresh_token` grant when it expires). Multiple connections to different accounts are just multiple entries with the same `url` and a different bearer token.

## Development

```sh
go test ./...   # unit tests against in-memory fake IMAP/SMTP servers + end-to-end MCP-over-HTTP tests
```

## Security notes

- Authentication is OAuth 2.0 only: credentials are captured once at consent time and carried encrypted inside the access token; the server keeps no account state and never logs them, and they never appear in URLs.
- OAuth access tokens are RS256 JWTs whose account claim is AES-256-GCM encrypted with `EMAIL_MCP_TOKEN_SECRET`; refresh tokens are random and rotated on use.
- TLS by default; the `imap_insecure` / `smtp_insecure` / `*_skip_verify` consent-form options are explicit opt-ins for local testing.
- Destructive tools are annotated (`delete_messages` has `destructiveHint`); keep a human in the loop in your client.
