// Package httpserver exposes mail accounts over MCP Streamable HTTP.
//
// Accounts can come from three sources, merged together:
//   - static config (env EMAIL_MCP_ACCOUNTS or accounts.json), served at
//     /{domain}
//   - per-request query params, e.g.
//     /gmail.com?email=user@gmail.com&imap_host=imap.gmail.com&imap_port=993&imap_tls=1
//     Server settings not given in the query are discovered automatically via
//     DNS SRV/MX records.
//   - per-request credentials via headers:
//     X-Email-Password: <password or app-password>   (custom header)
//     Authorization: Bearer <token>                  (OAuth-style; the token is
//     used as the mail credential, e.g. an XOAUTH2 access token)
//
// If no credential is provided this way, the server falls back to asking the
// user via MCP elicitation on the first tool call.
package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
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

// New returns an http.Handler routing /{domain} to the MCP Streamable HTTP
// endpoint of the matching account. The root path returns a JSON discovery
// document describing 0-config usage.
func New(byDomain map[string]*config.Account) http.Handler {
	var mu sync.Mutex
	servers := map[string]*mcp.Server{}
	stopCh := make(chan struct{})

	// serverFor builds (or reuses) the MCP server for a domain + query params.
	// On failure it records the error so getServer can report it in the response.
	var lastErr error
	serverFor := func(domain string, q map[string][]string, credential string) *mcp.Server {
		// Cache key: domain + server overrides + a hash of the credential
		// (the credential itself is never used as a key in cleartext).
		key := domain + "|" + cacheKey(q) + "|" + hashCredential(credential)
		mu.Lock()
		defer mu.Unlock()
		if s, ok := servers[key]; ok {
			return s
		}

		var acc *config.Account
		if a, ok := byDomain[domain]; ok && len(q) == 0 && credential == "" {
			acc = a // fully configured static account
		} else {
			var err error
			acc, err = buildAccount(domain, q, byDomain[domain])
			if err != nil {
				lastErr = err
				return nil
			}
			if credential != "" {
				// Header-provided credential wins over config; it is used
				// as-is for both IMAP and SMTP (XOAUTH2-style token or
				// plain password).
				acc.Password = credential
				acc.PasswordEnv = ""
				if acc.AuthMechanism == "" {
					acc.AuthMechanism = "plain"
				}
			}
		}

		s := mcpserver.New(acc)
		if sess := mcpserver.SessionOf(s); sess != nil {
			go sess.KeepAlive(stopCh)
		}
		servers[key] = s
		return s
	}

	// credentialFromRequest extracts the mail credential from the custom
	// header or the Authorization: Bearer header.
	credentialFromRequest := func(r *http.Request) string {
		if v := r.Header.Get(HeaderEmailPassword); v != "" {
			return v
		}
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			return strings.TrimPrefix(h, "Bearer ")
		}
		return ""
	}

	getServer := func(r *http.Request) *mcp.Server {
		domain := strings.Trim(r.URL.Path, "/")
		if domain == "" || strings.Contains(domain, "/") {
			return nil
		}
		domain = strings.ToLower(domain)
		return serverFor(domain, r.URL.Query(), credentialFromRequest(r))
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Logger: slog.Default(),
	})

	// Wrap so that account-build failures surface as a useful 400 response
	// instead of the SDK's generic "no server available".
	var wrapped http.Handler
	wrapped = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		domain := strings.Trim(r.URL.Path, "/")
		if domain != "" && !strings.Contains(domain, "/") && r.Method == http.MethodPost {
			mu.Lock()
			lastErr = nil
			mu.Unlock()
			getServer(r) // trigger build to populate lastErr
			mu.Lock()
			err := lastErr
			mu.Unlock()
			if err != nil {
				http.Error(w, "cannot set up account: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		mcpHandler.ServeHTTP(w, r)
	})

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
		// Optional server-level bearer token (protects the whole endpoint).
		// Distinct from the per-account mail credential above: if
		// EMAIL_MCP_AUTH_TOKEN is set, the Bearer header must match it and is
		// NOT used as a mail credential.
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
			w.Header().Set("Content-Type", "application/json")
			domains := make([]string, 0, len(byDomain))
			for domain := range byDomain {
				domains = append(domains, domain)
			}
			fmt.Fprintf(w, `{"service":"email-mcp","configured_accounts":[`)
			for i, d := range domains {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `"/%s"`, d)
			}
			fmt.Fprint(w, `],"zero_config":"/{domain}?email=user@domain — IMAP/SMTP settings are auto-discovered via DNS SRV/MX. Credentials: X-Email-Password header or Authorization: Bearer <token>, else elicitation. Optional overrides: imap_host, imap_port, imap_tls, imap_starttls, smtp_host, smtp_port, smtp_tls, smtp_starttls, username, display_name, auth_mechanism, sent_mailbox, drafts_mailbox"}`)
			return
		}
		wrapped.ServeHTTP(w, r)
	})
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func hashCredential(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// cacheKey builds a stable cache key from the server-related query params.
func cacheKey(q map[string][]string) string {
	keys := []string{"email", "username", "imap_host", "imap_port", "imap_tls", "imap_starttls", "smtp_host", "smtp_port", "smtp_tls", "smtp_starttls", "auth_mechanism"}
	var parts []string
	for _, k := range keys {
		if v := q[k]; len(v) > 0 {
			parts = append(parts, k+"="+v[0])
		}
	}
	return strings.Join(parts, "&")
}

// buildAccount merges: static config account for the domain (if any) as the
// base, query params on top, and DNS auto-discovery for anything still unset.
func buildAccount(domain string, q map[string][]string, base *config.Account) (*config.Account, error) {
	acc, err := config.FromQuery(q)
	if err != nil {
		// No email param: maybe a static account exists for this domain.
		if base != nil {
			cpy := *base
			return &cpy, nil
		}
		return nil, err
	}

	// Fill gaps from the static account.
	if base != nil {
		if acc.DisplayName == "" {
			acc.DisplayName = base.DisplayName
		}
		if acc.AuthMechanism == "" {
			acc.AuthMechanism = base.AuthMechanism
		}
		if acc.PasswordEnv == "" && base.PasswordEnv != "" {
			acc.PasswordEnv = base.PasswordEnv
		}
		if acc.Password == "" && base.Password != "" {
			acc.Password = base.Password
		}
		if acc.SentMailbox == "Sent" && base.SentMailbox != "Sent" {
			acc.SentMailbox = base.SentMailbox
		}
		if acc.DraftsMailbox == "Drafts" && base.DraftsMailbox != "Drafts" {
			acc.DraftsMailbox = base.DraftsMailbox
		}
	}

	// Auto-discover any missing server settings via DNS.
	needDiscovery := acc.IMAP.Host == "" || acc.SMTP.Host == ""
	if needDiscovery {
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
	return acc, nil
}
