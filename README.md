# email-mcp

A Go MCP server that exposes any IMAP/SMTP mail account to LLM hosts over **MCP Streamable HTTP**. The server is **stateless** and has a **single fixed endpoint** — the whole account setup travels in the client's request headers, which is how MCP clients with named connections (Mistral Vibe, VS Code `mcp.json`, Claude Desktop, ...) carry per-connection auth:

```
POST https://mcp.example.org/mcp
X-Email-Address: user@gmail.com
X-Email-Password: <app-password>        # or Authorization: ******<oauth2-token>
```

Current release: **v0.1.0** (see [CHANGELOG.md](CHANGELOG.md)).

## Usage

The MCP endpoint URL is always the same; each named connection differs only by its headers:

```
https://mcp.example.org/mcp
```

- **`X-Email-Address`** (required): the full mail address, e.g. `user@gmail.com`. Its domain drives DNS auto-discovery.
- **Credential** (setup-time auth, no elicitation needed):
  - `X-Email-Password: <password-or-app-password>` — custom header, works with any client that supports custom headers
  - `Authorization: ****** — OAuth-style bearer; the token is used directly as the mail credential (XOAUTH2, the default mechanism when only a Bearer token is given)
  - `/.well-known/oauth-protected-resource` (RFC 9728) is served so OAuth-aware clients can discover the accepted methods
  - if neither header is present, the server falls back to asking the user via MCP elicitation on the first tool call
- **Server settings are auto-discovered** from the address domain via DNS: `_imaps._tcp` / `_imap._tcp` and `_submissions._tcp` / `_submission._tcp` SRV records (RFC 6186), falling back to the MX host (RFC 7505) and then `imap.<domain>` / `smtp.<domain>` on the well-known ports.

Optional header overrides (unset = auto-discovered via DNS):

| Header | Meaning |
|---|---|
| `X-Email-Username` | login name override (default: the full address) |
| `X-Email-Display-Name` | From-header display name |
| `X-Email-Auth-Mechanism` | `plain` (default with password header), `login`, `xoauth2` (default with Bearer) |
| `X-Email-IMAP-Host`, `X-Email-IMAP-Port`, `X-Email-IMAP-TLS: true`, `X-Email-IMAP-STARTTLS: true`, `X-Email-IMAP-Insecure: true`, `X-Email-IMAP-Skip-Verify: true` | IMAP endpoint override |
| `X-Email-SMTP-Host`, `X-Email-SMTP-Port`, `X-Email-SMTP-TLS: true`, `X-Email-SMTP-STARTTLS: true`, `X-Email-SMTP-Insecure: true`, `X-Email-SMTP-Skip-Verify: true` | SMTP endpoint override |
| `X-Email-Sent-Mailbox`, `X-Email-Drafts-Mailbox` | folder names (default `Sent` / `Drafts`) |

`GET /` returns a JSON discovery document describing the convention. **Browsers** (requests with `Accept: text/html`) instead get a plain HTML setup form at `/`: fill in your email address and the optional overrides, submit, and the page shows the fixed endpoint URL plus a ready-to-copy client config with the corresponding `X-Email-*` headers (the password is *never* in the URL or the generated config — it stays a `${env:...}` placeholder).

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

There is **no server-side account configuration** — the server is stateless. Per request, the account is built from the `X-Email-*` request headers (table above); anything unset is auto-discovered via DNS, and the credential comes from `X-Email-Password` / `Authorization: Bearer` (else MCP elicitation).

The only server-level settings are deployment knobs: `-addr` / `EMAIL_MCP_ADDR` (default `:8080`) and the optional endpoint gate `EMAIL_MCP_AUTH_TOKEN` (if set, `Authorization: Bearer` must match this token and is then *not* used as a mail credential — use `X-Email-Password` for that).

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
    }
  }
}
```

(or `"Authorization": "******"` with `"X-Email-Auth-Mechanism": "xoauth2"` for an OAuth2 access token). Multiple connections to different accounts are just multiple entries with the same `url` and different headers.

## Development

```sh
go test ./...   # unit tests against in-memory fake IMAP/SMTP servers + end-to-end MCP-over-HTTP tests
```

## Security notes

- Credentials are carried only by client request headers (or elicitation); the server stores nothing and never logs them, and they never appear in URLs.
- TLS by default; the `X-Email-*-Insecure` / `X-Email-*-Skip-Verify` headers are explicit opt-ins for local testing.
- The HTTP endpoint can be gated with a deployment-level bearer token (`EMAIL_MCP_AUTH_TOKEN`).
- Destructive tools are annotated (`delete_messages` has `destructiveHint`); keep a human in the loop in your client.
