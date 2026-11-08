# Changelog

All notable changes to email-mcp are documented here. Versions follow
semantic versioning (git-flow: releases are tagged on `main`, e.g. `v0.1.0`).

## Unreleased

### Changed

- **Consent form hides the "local testing only" fields in production**: the
  `imap_insecure` / `imap_skip_verify` / `smtp_insecure` /
  `smtp_skip_verify` checkboxes on the OAuth consent page
  (`GET /oauth/authorize`) are now only rendered when the request arrives
  over plain HTTP (development); over TLS (production) they are omitted.
  The submitted form values are still honored if POSTed directly.

## v0.3.0

Minor release: consent-time account validation plus a cleaner internal
structure for the HTML pages.

### Added

- **Consent-time account validation**: the OAuth consent form now verifies
  the submitted account before issuing an authorization code, using only
  read-only, side-effect-free operations — DNS auto-discovery (with TCP
  reachability check) for unset servers, IMAP dial + LOGIN/AUTHENTICATE +
  `LIST "" "*"` + `STATUS INBOX`, and SMTP dial + EHLO + AUTH + NOOP + QUIT
  (nothing is ever sent). On failure the consent form is re-rendered (HTTP
  400) with the exact server error and the submitted values pre-filled
  (password never re-rendered), so a wrong password or bad server is
  reported immediately instead of surfacing as a 401 on the first
  `POST /mcp`.

### Changed

- **HTML pages are now separate template files**: the browser how-to page
  (`GET /`) and the OAuth consent form (`GET /oauth/authorize`) moved from
  raw string literals in Go into `internal/httpserver/templates/howto.html`
  and `internal/oauthserver/templates/consent.html`, loaded via `go:embed`
  + `template.ParseFS` and baked into the binary. No behavior change.

## v0.2.1

Patch release: fix the CI docker smoke test, which still asserted the old
header-config setup form (`<form`) on the browser page at `/`; it now checks
the OAuth how-to page and verifies that unauthenticated `POST /mcp` answers
`401` with a `WWW-Authenticate` OAuth challenge.

## v0.2.0

Second minor release. Authentication is now **OAuth 2.0 only**: the built-in
authorization server issues one bearer token per mail account and `POST /mcp`
verifies it statelessly.

### Changed

- **OAuth-only authentication**: `POST /mcp` now accepts only
  `Authorization: ****** <access token>` issued by the built-in OAuth 2.0
  authorization server. The `X-Email-*` custom-header path, the packed
  base64-JSON single-header bearer, the raw-bearer-as-XOAUTH2-credential
  fallback, MCP elicitation, and the `EMAIL_MCP_AUTH_TOKEN` deployment gate
  are removed. Missing or invalid tokens get `401` + a `WWW-Authenticate`
  challenge pointing at the RFC 9728 protected-resource metadata.
- The **OAuth consent form** (`GET|POST /oauth/authorize`) is now the single
  account-configuration surface; it gained the `imap_skip_verify` /
  `smtp_skip_verify` checkboxes so it covers the full option set (empty
  IMAP/SMTP settings are still auto-discovered via DNS).
- `GET /` no longer serves a header-config setup form: API clients get the
  JSON discovery document, browsers (`Accept: text/html`) get a short HTML
  page explaining the OAuth flow.

### Added

- **Built-in OAuth 2.0 authorization server** (`internal/oauthserver`): RFC
  8414 metadata, RFC 7591 dynamic client registration, authorization-code
  flow with PKCE (S256) and an HTML consent form, refresh-token grant
  (rotated), and JWKS. Access tokens are RS256 JWTs whose `account` claim is
  the full mail account, AES-256-GCM encrypted with `EMAIL_MCP_TOKEN_SECRET`
  — one bearer token per MCP connection carries all the connection info, and
  `/mcp` verifies it statelessly. New env knobs: `EMAIL_MCP_BASE_URL` (OAuth
  issuer) and `EMAIL_MCP_TOKEN_SECRET`.

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
