# email-mcp

A Go MCP server that exposes any IMAP/SMTP mail account to LLM hosts over **MCP Streamable HTTP** — in **0-config mode** by default.

## Zero-config usage (default)

No config file needed. Point the client at the domain of the mail account and pass the email address as a query param:

```
https://mcp.example.org/gmail.com?email=user@gmail.com
```

- **Server settings are auto-discovered** from DNS: `_imaps._tcp` / `_imap._tcp` and `_submissions._tcp` / `_submission._tcp` SRV records (RFC 6186), falling back to the MX host (RFC 7505) and then `imap.<domain>` / `smtp.<domain>` on the well-known ports.
- **Credentials come from the request headers** (setup-time auth, no elicitation needed):
  - `X-Email-Password: <password-or-app-password>` — custom header, works with any client that supports custom headers
  - `Authorization: Bearer <token>` — OAuth-style bearer; the token is used directly as the mail credential (e.g. an XOAUTH2 access token)
  - `/.well-known/oauth-protected-resource` (RFC 9728) is served so OAuth-aware clients can discover the accepted methods
  - if neither header is present, the server falls back to asking the user via MCP elicitation on the first tool call
- Optional query-param overrides:

| Param | Meaning |
|---|---|
| `email` | (required) user's mail address |
| `username` | login name (default: email) |
| `display_name` | From-header display name |
| `imap_host`, `imap_port`, `imap_tls=1`, `imap_starttls=1`, `imap_insecure=1` | IMAP endpoint override |
| `smtp_host`, `smtp_port`, `smtp_tls=1`, `smtp_starttls=1`, `smtp_insecure=1` | SMTP endpoint override |
| `auth_mechanism` | `plain` (default), `login`, `xoauth2` |
| `sent_mailbox`, `drafts_mailbox` | folder names (default `Sent` / `Drafts`) |

Example with explicit servers:

```
https://mcp.example.org/example.org?email=user@example.org&imap_host=mail.example.org&imap_port=993&imap_tls=1&smtp_host=smtp.example.org&smtp_port=465&smtp_tls=1
```

`GET /` returns a JSON discovery document describing the configured accounts and the zero-config convention. **Browsers** (requests with `Accept: text/html`) instead get a plain HTML setup form at `/`: fill in your email address and the optional overrides, submit, and the page shows the ready-to-copy endpoint URL (e.g. `https://mcp.example.org/gmail.com?email=user@gmail.com`) — the password is *never* in the URL, it goes in a request header (see above).

## Optional pre-configured accounts

Accounts can still be pinned via `EMAIL_MCP_ACCOUNTS` (inline JSON or a file path) or `~/.config/email-mcp/accounts.json`; those are served at `/{domain}` with no query params, and `password_env` indirection keeps secrets out of the file:

```json
{
  "accounts": [
    {
      "email": "user@gmail.com",
      "display_name": "User Name",
      "password_env": "GMAIL_APP_PASSWORD",
      "auth_mechanism": "plain",
      "imap": { "host": "imap.gmail.com", "port": 993, "tls": true },
      "smtp": { "host": "smtp.gmail.com", "port": 587 }
    }
  ]
}
```

Query params on a pre-configured domain are merged on top of the static entry.

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
- 15 tools (see `tools/list`), resources (`mail://mailboxes`, templates
  `mail://mailbox/{mailbox}`, `mail://message/{mailbox}/{uid}`,
  `mail://attachment/{mailbox}/{uid}/{part}`), and prompts
  (`triage_inbox`, `summarize_thread`, `draft_reply`, `find_unanswered`)

## Configuration

**By default the server is 0-config** (see above): any `/{domain}?email=...` works, with DNS auto-discovery and elicitation-based credentials.

Optionally, pin accounts via `EMAIL_MCP_ACCOUNTS` (inline JSON or a path to a JSON file) or `~/.config/email-mcp/accounts.json`:

```json
{
  "accounts": [
    {
      "email": "user@gmail.com",
      "display_name": "User Name",
      "username": "user@gmail.com",
      "password_env": "GMAIL_APP_PASSWORD",
      "auth_mechanism": "plain",
      "imap": { "host": "imap.gmail.com", "port": 993, "tls": true },
      "smtp": { "host": "smtp.gmail.com", "port": 587 },
      "sent_mailbox": "Sent",
      "drafts_mailbox": "Drafts"
    }
  ]
}
```

- `password_env` names an env var holding the password / app-password / OAuth2 token — the secret itself never appears in the config file. If no password is configured and no credential header is sent, the server asks the user via MCP elicitation.
- `auth_mechanism`: `plain` (default), `login`, or `xoauth2`.
- IMAP/SMTP: `tls: true` = implicit TLS (993/465); otherwise STARTTLS (143/587). `insecure: true` skips TLS entirely (local testing only).
- Optional `EMAIL_MCP_AUTH_TOKEN`: if set, the `Authorization: Bearer` header must match this server-level token (it is then *not* used as a mail credential); use `X-Email-Password` for the mail credential in that case.

## Run

```sh
go build -o email-mcp ./cmd/email-mcp
./email-mcp -addr :8080
# 0-config: https://mcp.example.org/gmail.com?email=user@gmail.com
# behind a reverse proxy that terminates TLS:
# https://mcp.example.org  ->  http://127.0.0.1:8080
```

Flags/env: `-addr` / `EMAIL_MCP_ADDR` (default `:8080`), `-config` / `EMAIL_MCP_CONFIG` (optional; without it the server runs 0-config).

### Docker

```sh
docker build -t email-mcp .
docker run -p 8080:8080 email-mcp
# 0-config: https://mcp.example.org/gmail.com?email=user@gmail.com
# (put a TLS-terminating reverse proxy in front for HTTPS)
# optional env: -e EMAIL_MCP_ACCOUNTS=... -e EMAIL_MCP_AUTH_TOKEN=...
```

The image is a multi-stage build (golang:1.27-alpine → distroless static, nonroot, `EXPOSE 8080`, entrypoint `/email-mcp`).

## Client config (VS Code Copilot)

```json
{
  "mcpServers": {
    "gmail": {
      "type": "http",
      "url": "https://mcp.example.org/gmail.com?email=user@gmail.com",
      "headers": {
        "X-Email-Password": "${env:GMAIL_APP_PASSWORD}"
      }
    }
  }
}
```

(or `"Authorization": "Bearer ${env:GMAIL_OAUTH_TOKEN"}` with `auth_mechanism=xoauth2`)

## Development

```sh
go test ./...   # unit tests against in-memory fake IMAP/SMTP servers + end-to-end MCP-over-HTTP tests
```

## Security notes

- Credentials only via env vars; never logged.
- TLS by default; `insecure`/`insecure_skip_verify` are explicit opt-ins for local testing.
- The HTTP endpoint can be protected with a bearer token (`EMAIL_MCP_AUTH_TOKEN`).
- Destructive tools are annotated (`delete_messages` has `destructiveHint`); keep a human in the loop in your client.
