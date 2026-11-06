package httpserver

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"email-mcp/internal/config"
)

// formField maps a setup-form input to the X-Email-* header it becomes in the
// generated client config. Checkboxes emit "true" when checked.
type formField struct {
	name   string // form input name
	header string // X-Email-* header (empty for the email field, handled separately)
	bool   bool   // checkbox: checked → header: "true"
}

// formFields lists the form inputs in display order. The single "email" input
// becomes the required X-Email-Address header.
var formFields = []formField{
	{name: "email"},
	{name: "username", header: config.HeaderUsername},
	{name: "display_name", header: config.HeaderDisplayName},
	{name: "auth_mechanism", header: config.HeaderAuthMech},
	{name: "imap_host", header: config.HeaderIMAPHost},
	{name: "imap_port", header: config.HeaderIMAPPort},
	{name: "imap_tls", header: config.HeaderIMAPTLS, bool: true},
	{name: "imap_starttls", header: config.HeaderIMAPStartTLS, bool: true},
	{name: "imap_insecure", header: config.HeaderIMAPInsecure, bool: true},
	{name: "imap_skip_verify", header: config.HeaderIMAPSkipVerify, bool: true},
	{name: "smtp_host", header: config.HeaderSMTPHost},
	{name: "smtp_port", header: config.HeaderSMTPPort},
	{name: "smtp_tls", header: config.HeaderSMTPTLS, bool: true},
	{name: "smtp_starttls", header: config.HeaderSMTPStartTLS, bool: true},
	{name: "smtp_insecure", header: config.HeaderSMTPInsecure, bool: true},
	{name: "smtp_skip_verify", header: config.HeaderSMTPSkipVerify, bool: true},
	{name: "sent_mailbox", header: config.HeaderSentMailbox},
	{name: "drafts_mailbox", header: config.HeaderDraftsMailbox},
}

var formPage = template.Must(template.New("form").Parse(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>email-mcp setup</title></head>
<body>
<h1>email-mcp &mdash; MCP endpoint for your mail account</h1>
{{if .Config}}
<p>The MCP endpoint URL is fixed; your account travels in the request
headers. Copy this into your client config (VS Code <code>mcp.json</code>,
Mistral Vibe connection, ...):</p>
<p><input type="text" readonly size="80" value="{{.Endpoint}}" onclick="this.select()"></p>
<pre>{{.Config}}</pre>
<p>The password is <strong>never</strong> part of the URL or this form. Set
<code>{{.PasswordHeader}}</code> to the real password / app password (or use
<code>Authorization: ******;token&gt;</code> with an OAuth2 access token);
<code>${env:...}</code> references above are just placeholders.
See <a href="/.well-known/oauth-protected-resource">/.well-known/oauth-protected-resource</a>
for the machine-readable auth metadata.</p>
<p><a href="/">&larr; build another connection</a></p>
{{else}}
<p>Fill in your account. The MCP endpoint URL stays fixed
(<code>{{.Endpoint}}</code>); these settings become the
<code>X-Email-*</code> headers of each request. Server settings are
auto-discovered from DNS when left empty. The password is not entered here;
you pass it via a header when connecting.</p>
<form method="get" action="/">
<fieldset>
<legend>Account</legend>
<p><label>email (required): <input type="text" name="email" placeholder="user@gmail.com" required></label>
&rarr; <code>{{.AddressHeader}}</code></p>
<p><label>username (login override, default: the email): <input type="text" name="username"></label></p>
<p><label>display_name: <input type="text" name="display_name"></label></p>
<p><label>auth_mechanism:
<select name="auth_mechanism">
<option value="">plain (default)</option>
<option value="login">login</option>
<option value="xoauth2">xoauth2</option>
</select></label></p>
</fieldset>
<fieldset>
<legend>IMAP (leave empty for DNS auto-discovery)</legend>
<p><label>imap_host: <input type="text" name="imap_host" placeholder="imap.example.org"></label>
<label>imap_port: <input type="text" name="imap_port" size="6" placeholder="993"></label></p>
<p><label><input type="checkbox" name="imap_tls" value="1"> imap_tls (implicit TLS, 993)</label>
<label><input type="checkbox" name="imap_starttls" value="1"> imap_starttls (143)</label>
<label><input type="checkbox" name="imap_insecure" value="1"> imap_insecure (local testing only)</label>
<label><input type="checkbox" name="imap_skip_verify" value="1"> imap_skip_verify (local testing only)</label></p>
</fieldset>
<fieldset>
<legend>SMTP (leave empty for DNS auto-discovery)</legend>
<p><label>smtp_host: <input type="text" name="smtp_host" placeholder="smtp.example.org"></label>
<label>smtp_port: <input type="text" name="smtp_port" size="6" placeholder="465"></label></p>
<p><label><input type="checkbox" name="smtp_tls" value="1"> smtp_tls (implicit TLS, 465)</label>
<label><input type="checkbox" name="smtp_starttls" value="1"> smtp_starttls (587)</label>
<label><input type="checkbox" name="smtp_insecure" value="1"> smtp_insecure (local testing only)</label>
<label><input type="checkbox" name="smtp_skip_verify" value="1"> smtp_skip_verify (local testing only)</label></p>
</fieldset>
<fieldset>
<legend>Folders</legend>
<p><label>sent_mailbox: <input type="text" name="sent_mailbox" placeholder="Sent"></label>
<label>drafts_mailbox: <input type="text" name="drafts_mailbox" placeholder="Drafts"></label></p>
</fieldset>
<p><button type="submit">Build client config</button></p>
</form>
{{end}}
</body>
</html>
`))

// wantsHTML reports whether the request comes from a browser that accepts
// HTML (so the root path can serve the setup form instead of the JSON
// discovery document).
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// serveRoot handles GET /: an HTML setup form for browsers (GET / with query
// params yields the ready-to-copy endpoint URL + client config), and the
// JSON discovery document for everything else.
func serveRoot(w http.ResponseWriter, r *http.Request) {
	if wantsHTML(r) {
		serveForm(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"service":"email-mcp","stateless":true,"endpoint":"`+MCPEndpoint+`","usage":"POST `+MCPEndpoint+` with headers X-Email-Address: user@gmail.com (required) and X-Email-Password or Authorization: Bearer for the credential; optional X-Email-IMAP-*/X-Email-SMTP-* overrides, auto-discovered via DNS SRV/MX when unset. Other optional headers: X-Email-Username, X-Email-Display-Name, X-Email-Auth-Mechanism, X-Email-Sent-Mailbox, X-Email-Drafts-Mailbox. Browsers: open / with an Accept: text/html header for a setup form"}`)
}

// serveForm renders the setup form (no query) or the result page (query
// present) for GET /.
func serveForm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	base := scheme(r) + "://" + r.Host
	endpoint := base + MCPEndpoint
	data := struct {
		Endpoint       string
		Config         string
		AddressHeader  string
		PasswordHeader string
	}{
		Endpoint:       endpoint,
		AddressHeader:  config.HeaderAddress,
		PasswordHeader: config.HeaderPassword,
	}
	if hasAnyParam(q) {
		data.Config = clientConfig(endpoint, q)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := formPage.Execute(w, data); err != nil {
		// The client is gone; nothing useful left to do.
		_ = err
	}
}

// hasAnyParam reports whether any known form param is present (including
// bare checkbox names, which arrive with an empty value).
func hasAnyParam(q url.Values) bool {
	for _, f := range formFields {
		if _, ok := q[f.name]; ok {
			return true
		}
	}
	return false
}

// clientConfig builds the copy-pasteable client config for the fixed MCP
// endpoint: the endpoint URL plus the X-Email-* headers derived from the form
// query params (email → X-Email-Address; the password header is a placeholder
// — passwords are never put in URLs or generated config).
func clientConfig(endpoint string, q url.Values) string {
	headers := map[string]string{}
	if email := q.Get("email"); email != "" {
		headers[config.HeaderAddress] = email
	}
	for _, f := range formFields {
		if f.name == "email" || f.header == "" {
			continue
		}
		if _, present := q[f.name]; !present {
			continue
		}
		if f.bool {
			if q.Get(f.name) != "" || presentBare(q, f.name) {
				headers[f.header] = "true"
			}
			continue
		}
		if v := q.Get(f.name); v != "" {
			headers[f.header] = v
		}
	}
	// Placeholder for the credential: real value comes from the user's env.
	headers[config.HeaderPassword] = "${env:EMAIL_PASSWORD}"

	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("{\n  \"mcpServers\": {\n    \"mail\": {\n")
	fmt.Fprintf(&sb, "      \"type\": \"http\",\n      \"url\": %q,\n      \"headers\": {\n", endpoint)
	for i, k := range keys {
		comma := ","
		if i == len(keys)-1 {
			comma = ""
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(headers[k])
		fmt.Fprintf(&sb, "        %s: %s%s\n", kb, vb, comma)
	}
	sb.WriteString("      }\n    }\n  }\n}")
	return sb.String()
}

// presentBare reports whether the param arrived as a bare checkbox name
// (?imap_tls with no =value).
func presentBare(q url.Values, key string) bool {
	vals, ok := q[key]
	return ok && len(vals) > 0 && vals[0] == ""
}
