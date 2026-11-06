// Package httpserver exposes mail accounts over MCP Streamable HTTP.
//
// The server is stateless and has a single fixed MCP endpoint: POST /mcp.
// There is no server-side account configuration and no per-account URL: the
// whole account description travels in the client's request headers (this is
// how MCP clients with named connections — e.g. Mistral Vibe, or VS Code
// mcp.json "headers" — carry per-connection auth):
//
//	X-Email-Address: user@gmail.com        (required; selects the account)
//	X-Email-Password: <password/app-pw>    (or Authorization: ******
//	                                        token used as the credential)
//	X-Email-IMAP-Host: imap.gmail.com      (optional overrides; anything
//	...                                     unset is auto-discovered via DNS)
//
// If no credential header is present, the server falls back to asking the
// user via MCP elicitation on the first tool call.
package httpserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/config"
	"email-mcp/internal/discovery"
	"email-mcp/internal/mcpserver"
)

// MCPEndpoint is the single Streamable HTTP endpoint all MCP clients use.
const MCPEndpoint = "/mcp"

// HeaderEmailPassword carries the mail credential as a custom HTTP header.
// Supported by any MCP client config that allows custom headers (VS Code
// mcp.json "headers", Mistral Vibe connections, Claude Desktop, curl, ...).
const HeaderEmailPassword = config.HeaderPassword

// New returns an http.Handler with a single MCP Streamable HTTP endpoint at
// /mcp; each request builds a fresh MCP server from its X-Email-* headers —
// no state is kept between requests. The root path serves a JSON discovery
// document (or the HTML setup form for browsers).
func New() http.Handler {
	// buildErr carries the last account-build failure out of getServer so
	// the wrapper can turn it into a useful 400 response. getServer may run
	// on concurrent requests, so access is mutex-guarded.
	var mu sync.Mutex
	var buildErr error
	setErr := func(err error) {
		mu.Lock()
		buildErr = err
		mu.Unlock()
	}
	getErr := func() error {
		mu.Lock()
		defer mu.Unlock()
		return buildErr
	}

	// getServer builds a fresh MCP server for each request from the
	// X-Email-* headers — no state is kept between requests.
	getServer := func(r *http.Request) *mcp.Server {
		acc, err := buildAccount(r.Header)
		if err != nil {
			setErr(err)
			return nil
		}
		setErr(nil)
		return mcpserver.New(acc)
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Logger: slog.Default(),
	})

	// Wrap so that account-build failures surface as a useful 400 response
	// instead of the SDK's generic "no server available".
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			setErr(nil)
			getServer(r) // trigger build to populate buildErr
			if err := getErr(); err != nil {
				http.Error(w, "cannot set up account: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		mcpHandler.ServeHTTP(w, r)
	})

	// Optional deployment-level bearer gate protecting the whole endpoint
	// (distinct from the per-request mail credential above: if set, the
	// Authorization: ****** must match it and is NOT a mail credential).
	authToken := os.Getenv("EMAIL_MCP_AUTH_TOKEN")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RFC 9728 protected-resource metadata so OAuth-aware clients can
		// discover how to authenticate to this resource.
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			resource := scheme(r) + "://" + r.Host + MCPEndpoint
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[],"scopes_supported":["email"],"bearer_methods_supported":["header"],"custom_headers":{"%s":"mail credential (password or app password)","%s":"full mail address, e.g. user@gmail.com"},"description":"email-mcp mail resource at %s; identify the account with %s and send the credential in the %s header or as a ******"}`,
				resource, HeaderEmailPassword, config.HeaderAddress, MCPEndpoint, config.HeaderAddress, HeaderEmailPassword)
			return
		}
		if authToken != "" {
			const prefix = "Bearer "
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, prefix) || strings.TrimPrefix(h, prefix) != authToken {
				w.Header().Set("WWW-Authenticate", `******"`+scheme(r)+`://`+r.Host+`/.well-known/oauth-protected-resource"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		switch r.URL.Path {
		case "/":
			// Browsers (Accept: text/html) get the HTML setup form at /;
			// other clients keep the JSON discovery document.
			serveRoot(w, r)
		case MCPEndpoint:
			wrapped.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
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

// buildAccount builds the account for one request: X-Email-* headers, then
// DNS auto-discovery for any server setting still unset, then the credential.
func buildAccount(h http.Header) (*config.Account, error) {
	acc, err := config.FromHeaders(h)
	if err != nil {
		return nil, err
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
