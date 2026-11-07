package httpserver

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"email-mcp/internal/oauthserver"
)

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

var howtoPage = template.Must(template.New("howto").Parse(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>email-mcp — setup</title></head>
<body>
<h1>email-mcp &mdash; MCP endpoint for your mail account</h1>
<p>The MCP endpoint URL is fixed: <code>{{.Endpoint}}</code>
(<code>POST {{.Endpoint | printf "%s"}}</code>). Authentication is
<strong>OAuth 2.0 only</strong>: email-mcp runs its own authorization server,
and the access token it issues carries your whole account, so the client
only needs a single <code>Authorization: ******</code> header.</p>
<ol>
<li>Register your MCP client once (RFC 7591):
<code>POST {{.Register}}</code> with <code>{"redirect_uris":["..."]}</code>.</li>
<li>Start the authorization flow:
<code>GET {{.Authorize}}?client_id=...&amp;redirect_uri=...&amp;state=...&amp;scope=email&amp;code_challenge=...&amp;code_challenge_method=S256</code>
&mdash; the consent form asks for your mail account (email + password/app
password, optional IMAP/SMTP overrides; empty servers are auto-discovered
via DNS).</li>
<li>Exchange the returned code at <code>POST {{.Token}}</code>
(grant <code>authorization_code</code> + PKCE verifier) for an access token
and a refresh token.</li>
<li>Configure your client with the fixed endpoint and
<code>Authorization: ****** &lt;access token&gt;</code>.</li>
</ol>
<p>Machine-readable discovery for OAuth-aware clients:
<a href="{{.ResourceMD}}">{{.ResourceMD}}</a> &rarr;
<a href="{{.ServerMD}}">{{.ServerMD}}</a>.</p>
</body>
</html>
`))
