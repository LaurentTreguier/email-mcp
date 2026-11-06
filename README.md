# email-mcp

A Go MCP server that exposes any IMAP/SMTP mail account to LLM hosts over **MCP Streamable HTTP**. The server is **stateless**: there is no server-side configuration at all — every request carries the full account description (path + query params + credential header).

## Usage

Point the client at the domain of the mail account (the URL path) and pass the user as a query param — the mail address is `username@domain`:

```
https://mcp.example.org/gmail.com?username=user        # user@gmail.com
https://mcp.example.org/gmail.com?username=user@gmail.com   # full address also works
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
| `username` | (required) local part (e.g. `user`) or full address matching the path domain; the mail address — and default login name — is `username@domain` |
| `display_name` | From-header display name |
| `imap_host`, `imap_port`, `imap_tls=1`, `imap_starttls=1`, `imap_insecure=1` | IMAP endpoint override |
| `smtp_host`, `smtp_port`, `smtp_tls=1`, `smtp_starttls=1`, `smtp_insecure=1` | SMTP endpoint override |
| `auth_mechanism` | `plain` (default), `login`, `xoauth2` |
| `sent_mailbox`, `drafts_mailbox` | folder names (default `Sent` / `Drafts`) |

Example with explicit servers:

```
https://mcp.example.org/example.org?username=user&imap_host=mail.example.org&imap_port=993&imap_tls=1&smtp_host=smtp.example.org&smtp_port=465&smtp_tls=1
```

`GET /` returns a JSON discovery document describing the stateless convention. **Browsers** (requests with `Accept: text/html`) instead get a plain HTML setup form at `/`: fill in your email address and the optional overrides, submit, and the page shows the ready-to-copy endpoint URL (e.g. `https://mcp.example.org/gmail.com?username=user`) — the email is split into path + `username`, and the password is *never* in the URL, it goes in a request header (see above).

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

There is **no server-side account configuration** — the server is stateless. Per request, the account is built from:

- the URL path domain (`/gmail.com`),
- the query params (table above): `username` (required), `display_name`, `auth_mechanism`, `imap_*`/`smtp_*` overrides, `sent_mailbox`, `drafts_mailbox` — anything unset is auto-discovered via DNS,
- the credential header (`X-Email-Password` or `Authorization: Bearer`), else MCP elicitation.

The only server-level settings are deployment knobs: `-addr` / `EMAIL_MCP_ADDR` (default `:8080`) and the optional endpoint gate `EMAIL_MCP_AUTH_TOKEN` (if set, `Authorization: Bearer` must match this token and is then *not* used as a mail credential — use `X-Email-Password` for that).

## Run

```sh
go build -o email-mcp ./cmd/email-mcp
./email-mcp -addr :8080
# stateless: https://mcp.example.org/gmail.com?username=user
# behind a reverse proxy that terminates TLS:
# https://mcp.example.org  ->  http://127.0.0.1:8080
```

Flags/env: `-addr` / `EMAIL_MCP_ADDR` (default `:8080`); optional `EMAIL_MCP_AUTH_TOKEN` to gate the whole endpoint.

### Docker

```sh
docker build -t email-mcp .
docker run -p 8080:8080 email-mcp
# stateless: https://mcp.example.org/gmail.com?username=user
# (put a TLS-terminating reverse proxy in front for HTTPS)
# optional env: -e EMAIL_MCP_AUTH_TOKEN=...
```

The image is a multi-stage build (golang:1.27-alpine → distroless static-debian13 nonroot, `EXPOSE 8080`, entrypoint `/email-mcp`).

## Client config (VS Code Copilot)

```json
{
  "mcpServers": {
    "gmail": {
      "type": "http",
      "url": "https://mcp.example.org/gmail.com?username=user",
      "headers": {
        "X-Email-Password": "${env:GMAIL_APP_PASSWORD}"
      }
    }
  }
}
```

(or `"Authorization": "Bearer ${env:OAUTH_TOKEN}"` with `auth_mechanism=xoauth2`)

## Development

```sh
go test ./...   # unit tests against in-memory fake IMAP/SMTP servers + end-to-end MCP-over-HTTP tests
```

## Security notes

- Credentials are carried only by client requests (header or elicitation); the server stores nothing and never logs them.
- TLS by default; `*_insecure` / `insecure_skip_verify` query params are explicit opt-ins for local testing.
- The HTTP endpoint can be gated with a deployment-level bearer token (`EMAIL_MCP_AUTH_TOKEN`).
- Destructive tools are annotated (`delete_messages` has `destructiveHint`); keep a human in the loop in your client.
