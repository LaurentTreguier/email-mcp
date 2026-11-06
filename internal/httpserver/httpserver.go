// Package httpserver exposes mail accounts over MCP Streamable HTTP.
//
// The server is stateless and has a single fixed MCP endpoint: POST /mcp.
// There is no server-side account configuration and no per-account URL. The
// whole account description travels in the client's request:
//
//   - Authorization: Bearer <JWT>   an access token issued by our own OAuth
//     authorization server (see internal/oauthserver); the account is embedded
//     (encrypted) in the token
//   - Authorization: Bearer <base64url(JSON account)>   a single header
//     packing the whole account (clients with only one auth header, e.g.
//     Mistral Vibe)
//   - X-Email-Address: user@gmail.com + X-Email-Password / raw Bearer token +
//     optional X-Email-* overrides (clients with custom-header support, e.g.
//     VS Code mcp.json "headers")
//
// If no credential is present, the server falls back to MCP elicitation on
// the first tool call. OAuth-aware clients discover the full OAuth flow via
// /.well-known/oauth-protected-resource → /.well-known/oauth-authorization-server.
package httpserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/config"
	"email-mcp/internal/discovery"
	"email-mcp/internal/mcpserver"
	"email-mcp/internal/oauthserver"
)

// MCPEndpoint is the single Streamable HTTP endpoint all MCP clients use.
const MCPEndpoint = "/mcp"

// HeaderEmailPassword carries the mail credential as a custom HTTP header.
// Supported by any MCP client config that allows custom headers (VS Code
// mcp.json "headers", Claude Desktop, curl, ...).
const HeaderEmailPassword = config.HeaderPassword

// New returns an http.Handler with a single MCP Streamable HTTP endpoint at
// /mcp; each request builds a fresh MCP server from its auth — no account
// state is kept between requests. The root path serves a JSON discovery
// document (or the HTML setup form for browsers), and the OAuth
// authorization-server endpoints are served under /oauth and /.well-known.
func New() http.Handler {
	return NewWithOAuth(nil)
}

// NewWithOAuth is like New but also serves the built-in OAuth 2.0
// authorization server when oauthOpts is non-nil. When nil, an authorization
// server is created lazily from EMAIL_MCP_BASE_URL (or per-request host) with
// a generated token secret.
func NewWithOAuth(oauthOpts *oauthserver.Options) http.Handler {
	// The built-in OAuth authorization server. BaseURL may be empty: the
	// issuer is then derived per request from scheme+host.
	var oauthSrv *oauthserver.Server
	if oauthOpts == nil {
		oauthOpts = &oauthserver.Options{}
		if v := os.Getenv("EMAIL_MCP_BASE_URL"); v != "" {
			oauthOpts.BaseURL = strings.TrimRight(v, "/")
		}
		if v := os.Getenv("EMAIL_MCP_TOKEN_SECRET"); v != "" {
			secret, err := base64.StdEncoding.DecodeString(v)
			if err != nil || len(secret) != 32 {
				// fall back to raw bytes if not base64
				secret = []byte(v)
			}
			if len(secret) > 32 {
				secret = secret[:32]
			}
			if len(secret) < 32 {
				padded := make([]byte, 32)
				copy(padded, secret)
				secret = padded
			}
			oauthOpts.TokenSecret = secret
		}
	}
	var err error
	oauthSrv, err = oauthserver.New(oauthOpts)
	if err != nil {
		// Without the OAuth server we can still run in raw-header mode.
		slog.Warn("oauth authorization server disabled", "err", err)
	}

	// wrapped serves POST /mcp: it builds the account once per request from
	// the request's auth (bearer JWT / packed bearer / X-Email-* headers),
	// then hands a fixed MCP server for that account to the SDK — no account
	// state is kept between requests. Build failures surface as 401 (with an
	// OAuth challenge) when no credential was presented, 400 when the account
	// description itself is invalid.
	// getServer builds a fresh MCP server per request from the request's
	// auth; the wrapper below only pre-checks the account build so failures
	// surface as 401/400 instead of the SDK's generic "no server available".
	getServer := func(r *http.Request) *mcp.Server {
		acc, err := buildAccount(r.Header, oauthSrv)
		if err != nil {
			return nil
		}
		return mcpserver.New(acc)
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Logger: slog.Default(),
	})

	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, err := buildAccount(r.Header, oauthSrv)
			if err != nil {
				slog.Warn("mcp account build failed", "path", r.URL.Path, "err", err)
				if isAuthMissing(err) {
					w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+scheme(r)+`://`+r.Host+`/.well-known/oauth-protected-resource"`)
					http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
					return
				}
				http.Error(w, "cannot set up account: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		mcpHandler.ServeHTTP(w, r)
	})

	// Optional deployment-level bearer gate protecting the whole endpoint
	// (distinct from the per-request mail credential: if set, the
	// Authorization: Bearer must match it and is NOT a mail credential).
	authToken := os.Getenv("EMAIL_MCP_AUTH_TOKEN")

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RFC 9728 protected-resource metadata so OAuth-aware clients can
		// discover how to authenticate to this resource; it points at our own
		// authorization server.
		if r.URL.Path == oauthserver.ResourceMetadataPath {
			base := scheme(r) + "://" + r.Host
			resource := base + MCPEndpoint
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q],"scopes_supported":["email"],"bearer_methods_supported":["header"],"description":"email-mcp mail resource at %s. Authenticate via OAuth (authorization server metadata at %s), or send the account in headers: %s (address) + %s (credential), or a single Authorization: Bearer base64url(JSON account)."}`,
				resource, base, MCPEndpoint, base+"/.well-known/oauth-authorization-server", config.HeaderAddress, HeaderEmailPassword)
			return
		}
		if authToken != "" {
			const prefix = "Bearer "
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, prefix) || strings.TrimPrefix(h, prefix) != authToken {
				w.Header().Set("WWW-Authenticate", `Bearer realm="email-mcp", resource_metadata="`+scheme(r)+`://`+r.Host+`/.well-known/oauth-protected-resource"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		switch {
		case r.URL.Path == "/":
			// Browsers (Accept: text/html) get the HTML setup form at /;
			// other clients keep the JSON discovery document.
			serveRoot(w, r)
		case r.URL.Path == MCPEndpoint:
			wrapped.ServeHTTP(w, r)
		default:
			// OAuth authorization-server endpoints.
			if oauthSrv != nil {
				oauthSrv.Handler().ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		}
	})
	return root
}

// isAuthMissing reports whether the build error is "no credential at all"
// (→ 401 with challenge) rather than a malformed account (→ 400).
func isAuthMissing(err error) bool {
	return err == errNoCredential
}

var errNoCredential = fmt.Errorf("no mail credential: use OAuth (see /.well-known/oauth-protected-resource), a single Authorization: Bearer base64url(JSON account) header, or X-Email-Address + X-Email-Password")

// scheme reports the scheme clients use to reach this server.
func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	return "http"
}

// buildAccount builds the account for one request. Resolution order:
//  1. Authorization: Bearer <JWT> issued by our OAuth server (account embedded)
//  2. Authorization: Bearer <base64url(JSON account)> single-header packing
//  3. X-Email-* headers (+ raw Bearer token as the mail credential)
//  4. none → errNoCredential (caller turns it into a 401 + OAuth challenge)
//
// Then DNS auto-discovery fills any unset IMAP/SMTP settings.
func buildAccount(h http.Header, oauthSrv *oauthserver.Server) (*config.Account, error) {
	var acc *config.Account
	var err error
	if a := h.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
		if oauthSrv != nil {
			if a2, err2 := oauthSrv.VerifyAccessToken(context.Background(), token); err2 == nil {
				acc = a2
			}
		}
		if acc == nil {
			acc = config.DecodeBearerAccount(token)
		}
		if acc == nil {
			// Raw token (e.g. an XOAUTH2 access token from the mail
			// provider): keep it as the credential; the account identity
			// then comes from the X-Email-Address header below.
			acc = &config.Account{Password: token, AuthMechanism: "xoauth2"}
		}
	}
	if acc == nil || acc.Email == "" {
		if h.Get("Authorization") == "" && h.Get(config.HeaderAddress) == "" {
			return nil, errNoCredential
		}
		acc, err = config.FromHeaders(h)
		if err != nil {
			return nil, err
		}
	}
	// No password yet is OK when we have an account identity: the MCP
	// server will elicit the credential on the first tool call. Only a
	// completely missing identity/credential is a 401.
	if acc.Email == "" {
		return nil, errNoCredential
	}

	// Auto-discover any missing server settings via DNS for the address domain.
	if acc.IMAP.Host == "" || acc.SMTP.Host == "" {
		domain := acc.Domain()
		discovered, derr := discovery.Discover(domain)
		if derr != nil {
			return nil, fmt.Errorf("auto-discovery for %s failed: %w (pass %s/%s etc. explicitly)", domain, derr, config.HeaderIMAPHost, config.HeaderSMTPHost)
		}
		if acc.IMAP.Host == "" {
			mode := "tls"
			if !discovered.IMAPTLS {
				mode = "starttls"
			}
			acc.SetIMAP(discovered.IMAPHost, discovered.IMAPPort, mode)
		}
		if acc.SMTP.Host == "" {
			mode := "tls"
			if !discovered.SMTPTLS {
				mode = "starttls"
			}
			acc.SetSMTP(discovered.SMTPHost, discovered.SMTPPort, mode)
		}
	}
	return acc, nil
}
