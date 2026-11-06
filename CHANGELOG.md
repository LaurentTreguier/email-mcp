# Changelog

All notable changes to email-mcp are documented here. Versions follow
semantic versioning (git-flow: releases are tagged on `main`, e.g. `v0.1.0`).

## v0.1.0

First minor release. A stateless Go MCP server exposing any IMAP/SMTP mail
account to LLM hosts over MCP Streamable HTTP.

### Added

- **Streamable HTTP transport** at `/{domain}` — the plain mail domain in the
  URL path selects the account (`https://host/gmail.com`).
- **Stateless / 0-config operation**: the account is built per request from
  the path domain + query params (`username` required, plus `display_name`,
  `auth_mechanism`, `imap_*`/`smtp_*` overrides, `sent_mailbox`,
  `drafts_mailbox`); unset server settings are auto-discovered via DNS
  (SRV RFC 6186, MX RFC 7505, well-known hosts) with a reachability check.
- **Setup-time credentials via request headers**: `X-Email-Password` custom
  header or `Authorization: Bearer` (used as the mail credential, e.g.
  XOAUTH2); RFC 9728 `/.well-known/oauth-protected-resource` metadata endpoint;
  MCP elicitation as fallback on the first tool call.
- **19 tools**: mailbox management (list/create/rename/delete/subscribe/
  unsubscribe/status), UID search, message listing/fetching, attachment
  download (base64), flag management, move/copy/delete (UID MOVE with
  fallback), quota, `send_email` (MIME compose + SMTP submission + Sent
  APPEND), `save_draft`.
- **Resources**: `mail://mailboxes` and templates `mail://mailbox/{mailbox}`,
  `mail://message/{mailbox}/{uid}`, `mail://attachment/{mailbox}/{uid}/{part}`.
- **Prompts**: `triage_inbox`, `summarize_thread`, `draft_reply`,
  `find_unanswered`.
- **Browser setup form** at `GET /` (`Accept: text/html`): builds a
  copy-pasteable endpoint URL from a single email field (split into path +
  `username`); plain clients get a JSON discovery document.
- **Dockerfile** (multi-stage, distroless static-debian13 nonroot) and CI
  (`.github/workflows/test.yml`: tidy/verify/gofmt/build/vet/test + docker
  build & smoke test) with weekly Dependabot updates.
- Tests against in-memory fake IMAP/SMTP servers plus end-to-end
  MCP-over-HTTP tests (query params + elicitation, header auth, Bearer auth,
  wrong-password rejection).

### Security

- Credentials only via request headers or elicitation — never in URLs, logs,
  or server state; TLS by default with explicit opt-in insecure flags for
  local testing; optional `EMAIL_MCP_AUTH_TOKEN` deployment-level endpoint
  gate.
