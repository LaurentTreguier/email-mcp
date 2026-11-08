package httpserver

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"email-mcp/internal/oauthserver"
)

//go:embed templates/howto.html
var templatesFS embed.FS

// wantsHTML reports whether the request comes from a browser that accepts
// HTML (so the root path can serve a short how-to page instead of the JSON
// discovery document).
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// serveRoot handles /: a JSON discovery document for API clients, and a
// short HTML page for browsers explaining the OAuth-only setup.
func serveRoot(w http.ResponseWriter, r *http.Request) {
	if wantsHTML(r) {
		base := scheme(r) + "://" + r.Host
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		howtoPage.Execute(w, map[string]string{ //nolint:errcheck
			"Base":       base,
			"Endpoint":   base + MCPEndpoint,
			"ResourceMD": base + oauthserver.ResourceMetadataPath,
			"ServerMD":   base + oauthserver.MetadataPath,
			"Register":   base + oauthserver.RegisterPath,
			"Authorize":  base + oauthserver.AuthorizePath,
			"Token":      base + oauthserver.TokenPath,
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"service":"email-mcp","stateless":true,"endpoint":"`+MCPEndpoint+`","usage":"POST `+MCPEndpoint+` with Authorization: ****** <access token>, where the token is an OAuth 2.0 access token issued by this server's built-in authorization server. Discovery: /.well-known/oauth-protected-resource -> /.well-known/oauth-authorization-server; dynamic client registration at /oauth/register; consent at /oauth/authorize; token at /oauth/token. Unset IMAP/SMTP servers are auto-discovered via DNS SRV/MX from the account domain. Browsers: open / with an Accept: text/html header for a how-to page"}`)
}

// howtoPage is the browser how-to page served at /, loaded from
// templates/howto.html (baked into the binary via embed).
var howtoPage = template.Must(template.ParseFS(templatesFS, "templates/howto.html"))
