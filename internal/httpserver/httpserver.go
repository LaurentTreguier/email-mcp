// Package httpserver exposes mail accounts over MCP Streamable HTTP.
//
// The server is stateless: no server-side account configuration exists.
// Every request fully describes the account:
//   - the URL path is the mail domain: /gmail.com
//   - query params carry the identity and server overrides, e.g.
//     /gmail.com?username=user&imap_host=imap.gmail.com&imap_port=993&imap_tls=1
//     (the mail address is username@domain: /gmail.com?username=user is
//     user@gmail.com). Server settings not given are auto-discovered via
//     DNS SRV/MX records.
//   - the credential comes in a request header:
//     X-Email-Password: <password or app-password>   (custom header)
//     Authorization: Bearer <token>                  (the token is used
//     as the mail credential, e.g. an XOAUTH2 access token)
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

// HeaderEmailPassword carries the mail credential as a custom HTTP header.
// Supported by any MCP client config that allows custom headers (VS Code
// mcp.json "headers", Claude Desktop, curl, ...).
const HeaderEmailPassword = "X-Email-Password"

// New returns an http.Handler routing /{domain}?{params} to a stateless MCP
// Streamable HTTP endpoint built per request. The root path serves a JSON
// discovery document (or the HTML setup form for browsers).
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

	// getServer builds a fresh MCP server for each request from the path,
	// query params and credential header — no state is kept between requests.
	getServer := func(r *http.Request) *mcp.Server {
		domain := strings.Trim(r.URL.Path, "/")
		if domain == "" || strings.Contains(domain, "/") {
			return nil
		}
		acc, err := buildAccount(strings.ToLower(domain), r.URL.Query(), credentialFromRequest(r))
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
		domain := strings.Trim(r.URL.Path, "/")
		if domain != "" && !strings.Contains(domain, "/") && r.Method == http.MethodPost {
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
	// Authorization: Bearer value must match it and is NOT a mail credential).
	authToken := os.Getenv("EMAIL_MCP_AUTH_TOKEN")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RFC 9728 protected-resource metadata so OAuth-aware clients can
		// discover how to authenticate to this resource.
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			resource := scheme(r) + "://" + r.Host
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[],"scopes_supported":["email"],"bearer_methods_supported":["header"],"custom_header":"%s","description":"email-mcp mail resource; send the mail credential in the %s header or as a Bearer token"}`,
				resource, HeaderEmailPassword, HeaderEmailPassword)
			return
		}
		if authToken != "" {
			const prefix = "Bearer "
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, prefix) || strings.TrimPrefix(h, prefix) != authToken {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+scheme(r)+`://`+r.Host+`/.well-known/oauth-protected-resource"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if strings.Trim(r.URL.Path, "/") == "" {
			// Browsers (Accept: text/html) get the HTML setup form at /;
			// other clients keep the JSON discovery document.
			serveRoot(w, r)
			return
		}
		wrapped.ServeHTTP(w, r)
	})
}

// credentialFromRequest extracts the mail credential from the custom header
// or the Authorization: Bearer token.
func credentialFromRequest(r *http.Request) string {
	if v := r.Header.Get(HeaderEmailPassword); v != "" {
		return v
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// buildAccount builds the account for one request: query params, then DNS
// auto-discovery for any server setting still unset, then the header
// credential.
func buildAccount(domain string, q map[string][]string, credential string) (*config.Account, error) {
	acc, err := config.FromQuery(domain, q)
	if err != nil {
		return nil, err
	}

	// Auto-discover any missing server settings via DNS.
	if acc.IMAP.Host == "" || acc.SMTP.Host == "" {
		discovered, derr := discovery.Discover(domain)
		if derr != nil {
			return nil, fmt.Errorf("auto-discovery for %s failed: %w (pass imap_host/imap_port/smtp_host/smtp_port explicitly)", domain, derr)
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

	if credential != "" {
		// The header credential is used as-is for both IMAP and SMTP
		// (XOAUTH2-style token or plain password).
		acc.Password = credential
		if acc.AuthMechanism == "" {
			acc.AuthMechanism = "plain"
		}
	}
	return acc, nil
}
