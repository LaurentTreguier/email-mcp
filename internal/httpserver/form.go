package httpserver

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// formParams lists the URL query options the setup form exposes, in display
// order. It mirrors the keys accepted by config.FromQuery.
// The form's single "email" input is split into the path domain and the
// "username" param when the endpoint URL is built (see endpointURL).
var formParams = []string{
	"email",
	"username",
	"display_name",
	"auth_mechanism",
	"imap_host",
	"imap_port",
	"imap_tls",
	"imap_starttls",
	"imap_insecure",
	"smtp_host",
	"smtp_port",
	"smtp_tls",
	"smtp_starttls",
	"smtp_insecure",
	"sent_mailbox",
	"drafts_mailbox",
}

var formPage = template.Must(template.New("form").Parse(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>email-mcp setup</title></head>
<body>
<h1>email-mcp &mdash; MCP endpoint for your mail account</h1>
{{if .URL}}
<p>Copy this MCP endpoint URL into your client config:</p>
<p><input type="text" readonly size="100" value="{{.URL}}" onclick="this.select()"></p>
<p>Or use this shorter path form:</p>
<p><input type="text" readonly size="100" value="{{.ShortURL}}" onclick="this.select()"></p>
<p>The password is <strong>never</strong> part of the URL. Send it in a
request header instead: <code>X-Email-Password: &lt;password&gt;</code> or
<code>Authorization: Bearer &lt;token&gt;</code>.
See <a href="/.well-known/oauth-protected-resource">/.well-known/oauth-protected-resource</a>
for the machine-readable auth metadata.</p>
<p><a href="/">&larr; build another endpoint</a></p>
{{else}}
<p>Fill in your account to build the MCP endpoint URL. Server settings are
auto-discovered from DNS when left empty. The password is not entered here;
you will pass it via a header when connecting.</p>
<form method="get" action="/">
<fieldset>
<legend>Account</legend>
<p><label>email (required): <input type="text" name="email" placeholder="user@gmail.com" required></label>
— split into the URL path (<code>/gmail.com</code>) and the
<code>username</code> param (<code>?username=user</code>)</p>
<p><label>display_name: <input type="text" name="display_name"></label></p>
<p><label>username override (default: the email): <input type="text" name="username"></label></p>
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
<label><input type="checkbox" name="imap_insecure" value="1"> imap_insecure (local testing only)</label></p>
</fieldset>
<fieldset>
<legend>SMTP (leave empty for DNS auto-discovery)</legend>
<p><label>smtp_host: <input type="text" name="smtp_host" placeholder="smtp.example.org"></label>
<label>smtp_port: <input type="text" name="smtp_port" size="6" placeholder="465"></label></p>
<p><label><input type="checkbox" name="smtp_tls" value="1"> smtp_tls (implicit TLS, 465)</label>
<label><input type="checkbox" name="smtp_starttls" value="1"> smtp_starttls (587)</label>
<label><input type="checkbox" name="smtp_insecure" value="1"> smtp_insecure (local testing only)</label></p>
</fieldset>
<fieldset>
<legend>Folders</legend>
<p><label>sent_mailbox: <input type="text" name="sent_mailbox" placeholder="Sent"></label>
<label>drafts_mailbox: <input type="text" name="drafts_mailbox" placeholder="Drafts"></label></p>
</fieldset>
<p><button type="submit">Build endpoint URL</button></p>
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
// params yields the ready-to-copy endpoint URL), and the JSON discovery
// document for everything else.
func serveRoot(w http.ResponseWriter, r *http.Request) {
	if wantsHTML(r) {
		serveForm(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"service":"email-mcp","stateless":true,"usage":"/{domain}?username=user — the mail address is username@domain. IMAP/SMTP settings are auto-discovered via DNS SRV/MX. Credentials: X-Email-Password header or Authorization: Bearer, else elicitation. Optional overrides: imap_host, imap_port, imap_tls, imap_starttls, smtp_host, smtp_port, smtp_tls, smtp_starttls, display_name, auth_mechanism, sent_mailbox, drafts_mailbox. Browsers: open / with an Accept: text/html header for a setup form"}`)
}

// serveForm renders the setup form (no query) or the result page (query
// present) for GET /.
func serveForm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	base := scheme(r) + "://" + r.Host
	data := struct {
		URL      string
		ShortURL string
	}{}
	if hasAnyParam(q) {
		u := endpointURL(base, q)
		data.URL = u
		data.ShortURL = strings.TrimPrefix(u, base)
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
	for _, k := range formParams {
		if _, ok := q[k]; ok {
			return true
		}
	}
	return false
}

// endpointURL builds the cleaned MCP endpoint URL from the form query params:
// scheme://host/{domain}?{non-empty known params, sorted}. The form's email
// field is split into the path domain and the username param (an explicit
// username field wins); the email param itself is never emitted. Unknown
// params are dropped.
func endpointURL(base string, q url.Values) string {
	email := q.Get("email")
	domain := ""
	local := ""
	if l, dom, ok := strings.Cut(email, "@"); ok {
		local, domain = l, strings.ToLower(dom)
	}
	vals := url.Values{}
	if q.Get("username") != "" {
		vals.Set("username", q.Get("username"))
	} else if local != "" {
		vals.Set("username", local)
	}
	for _, k := range formParams {
		if k == "email" {
			continue // consumed above: becomes path + username
		}
		if v := q.Get(k); v != "" {
			vals.Set(k, v)
		}
	}
	// Checkbox params may arrive as bare names (?imap_tls) — normalize to =1.
	for _, k := range []string{"imap_tls", "imap_starttls", "imap_insecure", "smtp_tls", "smtp_starttls", "smtp_insecure"} {
		if _, present := q[k]; present && q.Get(k) == "" {
			vals.Set(k, "1")
		}
	}
	enc := vals.Encode() // sorted, URL-escaped
	u := base + "/" + domain
	if enc != "" {
		u += "?" + enc
	}
	return u
}
