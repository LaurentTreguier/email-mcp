// Package httpserver exposes mail accounts over MCP Streamable HTTP.
//
// The server is stateless and has a single fixed MCP endpoint: POST /mcp.
// There is no server-side account configuration and no per-account URL.
// Authentication is OAuth 2.0 only: clients present
//
//	Authorization: ****** <access token>
//
// where the token is an access token issued by our own OAuth authorization
// server (see internal/oauthserver); the whole account description is
// embedded (encrypted) in the token. OAuth-aware clients discover the flow
// via /.well-known/oauth-protected-resource ->
// /.well-known/oauth-authorization-server.
package httpserver

import (
	"bytes"
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

// New returns an http.Handler with a single MCP Streamable HTTP endpoint at
// /mcp; each request builds a fresh MCP server from its OAuth bearer token —
// no account state is kept between requests. The root path serves a discovery
// document (JSON, or a short HTML how-to for browsers), and the OAuth
// authorization-server endpoints are served under /oauth and /.well-known.
//
// It fails when the runtime configuration needed for restart-stable tokens is
// missing (EMAIL_MCP_SIGNING_KEY or EMAIL_MCP_SIGNING_KEY_FILE, and
// EMAIL_MCP_TOKEN_SECRET): without them every restart would invalidate all
// issued access tokens.
func New() (http.Handler, error) {
	return NewWithOAuth(nil)
}

// NewWithOAuth is like New but takes explicit OAuth options. When nil, the
// options are built from the environment: EMAIL_MCP_BASE_URL (issuer, else
// derived per request), EMAIL_MCP_SIGNING_KEY / EMAIL_MCP_SIGNING_KEY_FILE
// (RSA private key, required — the inline variant takes PEM text or
// base64-encoded PEM since PEM's newlines are awkward in env vars; the file
// variant takes precedence and is meant for mounted secrets), and
// EMAIL_MCP_TOKEN_SECRET (required 32-byte secret: a plain 32-char random
// string is used raw; otherwise the value is base64-decoded).
func NewWithOAuth(oauthOpts *oauthserver.Options) (http.Handler, error) {
	// The built-in OAuth authorization server. BaseURL may be empty: the
	// issuer is then derived per request from scheme+host.
	var oauthSrv *oauthserver.Server
	if oauthOpts == nil {
		oauthOpts = &oauthserver.Options{}
		if v := os.Getenv("EMAIL_MCP_BASE_URL"); v != "" {
			oauthOpts.BaseURL = strings.TrimRight(v, "/")
		}
		// Signing key: PEM inline (optionally base64-encoded), or read from
		// a file (mounted secret).
		if v := os.Getenv("EMAIL_MCP_SIGNING_KEY"); v != "" {
			pemBytes := []byte(v)
			if !bytes.Contains(pemBytes, []byte("-----BEGIN")) {
				// No PEM header: assume base64-encoded PEM (env vars don't
				// carry newlines well).
				if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v)); err == nil {
					pemBytes = dec
				}
			}
			oauthOpts.SigningKeyPEM = pemBytes
		}
		if v := os.Getenv("EMAIL_MCP_SIGNING_KEY_FILE"); v != "" {
			pemBytes, err := os.ReadFile(v)
			if err != nil {
				return nil, fmt.Errorf("read EMAIL_MCP_SIGNING_KEY_FILE: %w", err)
			}
			oauthOpts.SigningKeyPEM = pemBytes
		}
		if v := os.Getenv("EMAIL_MCP_TOKEN_SECRET"); v != "" {
			// A plain random string is used raw; only fall back to base64
			// when the raw value is not exactly 32 bytes.
			secret := []byte(v)
			if len(secret) != 32 {
				if dec, err := base64.StdEncoding.DecodeString(v); err == nil {
					secret = dec
				}
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
		// OAuth is the only auth path: without the authorization server no
		// /mcp request can be authenticated. Fail fast at startup instead of
		// serving a server whose tokens die on the next restart.
		return nil, fmt.Errorf("oauth authorization server: %w", err)
	}

	// wrapped serves POST /mcp: it builds the account once per request from
	// the request's OAuth bearer token, then hands a fixed MCP server for
	// that account to the SDK — no account state is kept between requests.
	// Auth failures surface as 401 with an OAuth challenge.
	// getServer builds a fresh MCP server per request from the request's
	// bearer token; the wrapper below only pre-checks the token so failures
	// surface as 401 instead of the SDK's generic "no server available".
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
			if _, err := buildAccount(r.Header, oauthSrv); err != nil {
				slog.Warn("mcp auth failed", "path", r.URL.Path, "err", err)
				w.Header().Set("WWW-Authenticate", `******"email-mcp", resource_metadata="`+scheme(r)+`://`+r.Host+`/.well-known/oauth-protected-resource"`)
				http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
				return
			}
		}
		mcpHandler.ServeHTTP(w, r)
	})

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RFC 9728 protected-resource metadata so OAuth-aware clients can
		// discover how to authenticate to this resource; it points at our own
		// authorization server.
		if r.URL.Path == oauthserver.ResourceMetadataPath {
			base := scheme(r) + "://" + r.Host
			resource := base + MCPEndpoint
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q],"scopes_supported":["email"],"bearer_methods_supported":["header"],"description":"email-mcp mail resource at %s. Authenticate with an OAuth 2.0 access token issued by the authorization server (metadata at %s)."}`,
				resource, base, MCPEndpoint, base+"/.well-known/oauth-authorization-server")
			return
		}
		switch {
		case r.URL.Path == "/":
			// Browsers (Accept: text/html) get a short HTML how-to page at /;
			// other clients keep the JSON discovery document.
			serveRoot(w, r)
		case r.URL.Path == MCPEndpoint:
			wrapped.ServeHTTP(w, r)
		default:
			// OAuth authorization-server endpoints.
			oauthSrv.Handler().ServeHTTP(w, r)
		}
	})
	return root, nil
}

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

// buildAccount builds the account for one request from its OAuth bearer
// token: the token must be an access token issued by our own authorization
// server (account embedded, encrypted). Anything else — missing header,
// malformed or unverifiable token — is an auth error the caller turns into
// a 401 with an OAuth challenge.
//
// Then DNS auto-discovery fills any unset IMAP/SMTP settings.
func buildAccount(h http.Header, oauthSrv *oauthserver.Server) (*config.Account, error) {
	token := strings.TrimSpace(strings.TrimPrefix(h.Get("Authorization"), "Bearer "))
	if token == "" || oauthSrv == nil {
		return nil, fmt.Errorf("missing or invalid Authorization bearer token (run the OAuth flow, see /.well-known/oauth-protected-resource)")
	}
	acc, err := oauthSrv.VerifyAccessToken(context.Background(), token)
	if err != nil {
		return nil, fmt.Errorf("invalid access token: %w", err)
	}
	if acc.Email == "" {
		return nil, fmt.Errorf("token carries no account")
	}

	// Auto-discover any missing server settings via DNS for the address domain.
	if err := discovery.FillDefaults(acc); err != nil {
		return nil, err
	}
	return acc, nil
}
