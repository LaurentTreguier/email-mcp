package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"email-mcp/internal/config"
)

func get(t *testing.T, h http.Handler, path, accept string) (int, string, http.Header) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

func TestRootFormForBrowser(t *testing.T) {
	h := New()
	code, body, hdr := get(t, h, "/", "text/html,application/xhtml+xml")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	for _, field := range []string{
		`name="email"`, `name="display_name"`, `name="username"`,
		`name="auth_mechanism"`, `name="imap_host"`, `name="imap_port"`,
		`name="imap_tls"`, `name="imap_starttls"`, `name="imap_insecure"`,
		`name="imap_skip_verify"`,
		`name="smtp_host"`, `name="smtp_port"`, `name="smtp_tls"`,
		`name="smtp_starttls"`, `name="smtp_insecure"`,
		`name="smtp_skip_verify"`,
		`name="sent_mailbox"`, `name="drafts_mailbox"`,
	} {
		if !strings.Contains(body, field) {
			t.Errorf("form missing field %s", field)
		}
	}
	if !strings.Contains(body, "<form") {
		t.Error("form page has no <form> element")
	}
	// The form must explain the fixed endpoint and header convention.
	if !strings.Contains(body, MCPEndpoint) || !strings.Contains(body, config.HeaderAddress) {
		t.Error("form page should mention the fixed /mcp endpoint and X-Email-Address")
	}
}

func TestRootResultPageWithQuery(t *testing.T) {
	h := New()
	code, body, _ := get(t, h, "/?email=user@Gmail.com&imap_host=mail.example.org&smtp_port=&bogus=1", "text/html")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// Fixed endpoint URL is shown.
	if !strings.Contains(body, "/mcp") {
		t.Errorf("result page missing the fixed /mcp endpoint:\n%s", body)
	}
	// Email becomes the X-Email-Address header; empty/unknown params are
	// stripped; the password header is a placeholder, never a real password.
	// (The <pre> block is HTML-escaped, so quotes appear as &#34;.)
	if !strings.Contains(body, `&#34;X-Email-Address&#34;: &#34;user@Gmail.com&#34;`) {
		t.Errorf("result page missing X-Email-Address header:\n%s", body)
	}
	if !strings.Contains(body, `&#34;X-Email-IMAP-Host&#34;: &#34;mail.example.org&#34;`) {
		t.Errorf("result page missing X-Email-IMAP-Host header:\n%s", body)
	}
	if !strings.Contains(body, `&#34;X-Email-Password&#34;: &#34;${env:EMAIL_PASSWORD}&#34;`) {
		t.Errorf("result page should contain the password placeholder:\n%s", body)
	}
	if strings.Contains(body, "smtp_port") || strings.Contains(body, "bogus") {
		t.Errorf("result page should strip empty/unknown params:\n%s", body)
	}
	if strings.Contains(body, "email=user") {
		t.Errorf("result page must not put the raw email query in a URL:\n%s", body)
	}
	if !strings.Contains(body, config.HeaderPassword) {
		t.Errorf("result page should remind about the credential header:\n%s", body)
	}
}

func TestRootJSONDiscoveryUnchanged(t *testing.T) {
	h := New()
	// No Accept header (like http.Get): JSON discovery document.
	code, body, hdr := get(t, h, "/", "")
	if code != 200 || !strings.Contains(hdr.Get("Content-Type"), "application/json") {
		t.Fatalf("status=%d ct=%q", code, hdr.Get("Content-Type"))
	}
	if !strings.Contains(body, `"stateless":true`) || !strings.Contains(body, `"endpoint":"/mcp"`) {
		t.Fatalf("discovery doc should describe stateless /mcp usage: %s", body)
	}
	// Explicit JSON accept also gets JSON.
	_, body2, _ := get(t, h, "/", "application/json")
	if !strings.Contains(body2, `"service":"email-mcp"`) {
		t.Fatalf("json accept should get discovery doc: %s", body2)
	}
}

func TestClientConfig(t *testing.T) {
	q := map[string][]string{
		"email":         {"User@Example.ORG"},
		"smtp_tls":      {"1"},
		"imap_host":     {"mail.example.org"},
		"display_name":  {""},
		"imap_starttls": {""}, // bare checkbox
		"evil":          {"<script>"},
	}
	cfg := clientConfig("https://mcp.example.org/mcp", q)
	for _, want := range []string{
		`"url": "https://mcp.example.org/mcp"`,
		`"X-Email-Address": "User@Example.ORG"`,
		`"X-Email-IMAP-Host": "mail.example.org"`,
		`"X-Email-SMTP-TLS": "true"`,
		`"X-Email-IMAP-STARTTLS": "true"`,
		`"X-Email-Password": "${env:EMAIL_PASSWORD}"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("clientConfig missing %s:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "evil") || strings.Contains(cfg, "display_name") {
		t.Errorf("clientConfig should strip unknown/empty params:\n%s", cfg)
	}
}

func TestClientConfigUsernameOverride(t *testing.T) {
	q := map[string][]string{
		"email":    {"user@example.org"},
		"username": {"login-name"},
	}
	cfg := clientConfig("https://mcp.example.org/mcp", q)
	if !strings.Contains(cfg, `"X-Email-Address": "user@example.org"`) {
		t.Errorf("address header missing:\n%s", cfg)
	}
	if !strings.Contains(cfg, `"X-Email-Username": "login-name"`) {
		t.Errorf("username override header missing:\n%s", cfg)
	}
}
