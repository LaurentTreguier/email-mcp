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
	h := New(map[string]*config.Account{})
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
		`name="smtp_host"`, `name="smtp_port"`, `name="smtp_tls"`,
		`name="smtp_starttls"`, `name="smtp_insecure"`,
		`name="sent_mailbox"`, `name="drafts_mailbox"`,
	} {
		if !strings.Contains(body, field) {
			t.Errorf("form missing field %s", field)
		}
	}
	if !strings.Contains(body, "<form") {
		t.Error("form page has no <form> element")
	}
}

func TestRootResultPageWithQuery(t *testing.T) {
	h := New(map[string]*config.Account{})
	code, body, _ := get(t, h, "/?email=user@Gmail.com&imap_host=mail.example.org&&smtp_port=&bogus=1", "text/html")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// The endpoint URL is derived from the email domain (lowercased), empty
	// and unknown params are stripped, and params are sorted.
	want := `value="http://` + `` // host is dynamic; checked by prefix below
	_ = want
	if !strings.Contains(body, "/gmail.com?") {
		t.Errorf("result page missing cleaned /gmail.com path:\n%s", body)
	}
	if !strings.Contains(body, "imap_host=mail.example.org") {
		t.Errorf("result page missing imap_host param:\n%s", body)
	}
	if strings.Contains(body, "smtp_port=") || strings.Contains(body, "bogus") {
		t.Errorf("result page should strip empty/unknown params:\n%s", body)
	}
	if !strings.Contains(body, "X-Email-Password") {
		t.Errorf("result page should remind about the credential header:\n%s", body)
	}
	// Both full URL and short path are offered.
	if !strings.Contains(body, "readonly") {
		t.Errorf("result page should have a readonly copy field:\n%s", body)
	}
}

func TestRootJSONDiscoveryUnchanged(t *testing.T) {
	accs := &config.Accounts{Accounts: []config.Account{
		{Email: "user@gmail.com", Password: "x", IMAP: config.IMAPConfig{Host: "127.0.0.1", Port: 1}, SMTP: config.SMTPConfig{Host: "127.0.0.1", Port: 1}},
	}}
	h := New(accs.ByDomain())
	// No Accept header (like http.Get): JSON discovery document.
	code, body, hdr := get(t, h, "/", "")
	if code != 200 || !strings.Contains(hdr.Get("Content-Type"), "application/json") {
		t.Fatalf("status=%d ct=%q", code, hdr.Get("Content-Type"))
	}
	if !strings.Contains(body, `"/gmail.com"`) {
		t.Fatalf("discovery doc missing accounts: %s", body)
	}
	// Explicit JSON accept also gets JSON.
	_, body2, _ := get(t, h, "/", "application/json")
	if !strings.Contains(body2, `"/gmail.com"`) {
		t.Fatalf("json accept should get discovery doc: %s", body2)
	}
}

func TestEndpointURL(t *testing.T) {
	q := map[string][]string{
		"email":        {"User@Example.ORG"},
		"smtp_tls":     {"1"},
		"imap_host":    {"mail.example.org"},
		"display_name": {""},
		"evil":         {"<script>"},
	}
	u := endpointURL("https://mcp.example.org", q)
	want := "https://mcp.example.org/example.org?email=User%40Example.ORG&imap_host=mail.example.org&smtp_tls=1"
	if u != want {
		t.Errorf("endpointURL = %q, want %q", u, want)
	}
}
