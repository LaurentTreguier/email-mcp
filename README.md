# email-mcp

A Go MCP server that exposes any IMAP/SMTP mail account to LLM hosts over **MCP Streamable HTTP**.

One process serves many accounts. Each account is reachable at a path named after the **plain domain** of its email address:

```
https://mcp.example.org/gmail.com      -> user@gmail.com
https://mcp.example.org/example.org    -> someone@example.org
```

`GET /` returns a JSON discovery document listing the available account paths.

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

Accounts come from `EMAIL_MCP_ACCOUNTS` (inline JSON or a path to a JSON file)
or `~/.config/email-mcp/accounts.json`:

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

- `password_env` names an env var holding the password / app-password / OAuth2 token — the secret itself never appears in the config file.
- `auth_mechanism`: `plain` (default), `login`, or `xoauth2`.
- IMAP/SMTP: `tls: true` = implicit TLS (993/465); otherwise STARTTLS (143/587). `insecure: true` skips TLS entirely (local testing only).
- Optional `EMAIL_MCP_AUTH_TOKEN`: if set, all HTTP requests must carry `Authorization: Bearer <token>`.

## Run

```sh
go build -o email-mcp ./cmd/email-mcp
EMAIL_MCP_ACCOUNTS='{...}' ./email-mcp -addr :8080
# behind a reverse proxy that terminates TLS:
# https://mcp.example.org/gmail.com  ->  http://127.0.0.1:8080/gmail.com
```

Flags/env: `-addr` / `EMAIL_MCP_ADDR` (default `:8080`), `-config` / `EMAIL_MCP_CONFIG`.

## Client config (VS Code Copilot)

```json
{
  "mcpServers": {
    "gmail": {
      "type": "http",
      "url": "https://mcp.example.org/gmail.com"
    }
  }
}
```

## Development

```sh
go test ./...   # unit tests against in-memory fake IMAP/SMTP servers + end-to-end MCP-over-HTTP tests
```

## Security notes

- Credentials only via env vars; never logged.
- TLS by default; `insecure`/`insecure_skip_verify` are explicit opt-ins for local testing.
- The HTTP endpoint can be protected with a bearer token (`EMAIL_MCP_AUTH_TOKEN`).
- Destructive tools are annotated (`delete_messages` has `destructiveHint`); keep a human in the loop in your client.
