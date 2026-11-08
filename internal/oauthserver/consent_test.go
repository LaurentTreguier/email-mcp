package oauthserver

import (
	"context"
	"crypto/sha256"
	"email-mcp/internal/config"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestConsentFormFields checks the consent page exposes the full account
// configuration surface (parity with the old setup form).
func TestConsentFormFields(t *testing.T) {
	oldVerify := VerifyAccount
	VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { VerifyAccount = oldVerify })
	_, srv := newTestServer(t)

	// Register a client.
	resp, err := http.Post(srv.URL+RegisterPath, "application/json",
		strings.NewReader(`{"redirect_uris":["http://localhost/cb"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	json.NewDecoder(resp.Body).Decode(&reg) //nolint:errcheck
	resp.Body.Close()

	verifier := "v"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{
		"client_id":             {reg.ClientID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	resp, err = http.Get(srv.URL + AuthorizePath + "?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	// The test server's host is a loopback IP (127.0.0.1) = local
	// development: the "local testing only" insecure/skip-verify
	// checkboxes are shown.
	for _, field := range []string{
		`name="email"`, `name="password"`, `name="username"`, `name="display_name"`,
		`name="auth_mechanism"`,
		`name="imap_host"`, `name="imap_port"`, `name="imap_tls"`, `name="imap_starttls"`,
		`name="imap_insecure"`, `name="imap_skip_verify"`,
		`name="smtp_host"`, `name="smtp_port"`, `name="smtp_tls"`, `name="smtp_starttls"`,
		`name="smtp_insecure"`, `name="smtp_skip_verify"`,
		`name="sent_mailbox"`, `name="drafts_mailbox"`,
	} {
		if !strings.Contains(page, field) {
			t.Errorf("consent form missing field %s", field)
		}
	}
}

// consentPageFor fetches the authorize page and returns the rendered HTML.
// host, if non-empty, overrides the request Host header; fwdProto, if
// non-empty, sets X-Forwarded-Proto.
func consentPageFor(t *testing.T, client *http.Client, baseURL string, reg consentClient, host, fwdProto string) string {
	t.Helper()
	verifier := "v"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{
		"client_id":             {reg.ClientID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+AuthorizePath+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if fwdProto != "" {
		req.Header.Set("X-Forwarded-Proto", fwdProto)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return string(body)
}

type consentClient struct {
	ClientID string `json:"client_id"`
}

func registerClient(t *testing.T, client *http.Client, baseURL string) consentClient {
	t.Helper()
	resp, err := client.Post(baseURL+RegisterPath, "application/json",
		strings.NewReader(`{"redirect_uris":["http://localhost/cb"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg consentClient
	json.NewDecoder(resp.Body).Decode(&reg) //nolint:errcheck
	resp.Body.Close()
	return reg
}

// TestConsentFormHidesInsecureFieldsOverTLS checks that when clients reach
// the server over TLS (directly, or via a reverse proxy's
// X-Forwarded-Proto: https) the consent form does not expose the
// "local testing only" insecure/skip-verify checkboxes, while the regular
// fields remain.
func TestConsentFormHidesInsecureFieldsOverTLS(t *testing.T) {
	oldVerify := VerifyAccount
	VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { VerifyAccount = oldVerify })

	// Direct TLS.
	s, err := New(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	tlsSrv := httptest.NewTLSServer(s.Handler())
	t.Cleanup(tlsSrv.Close)
	tlsReg := registerClient(t, tlsSrv.Client(), tlsSrv.URL)

	// Plain HTTP with X-Forwarded-Proto: https (production behind a
	// TLS-terminating reverse proxy).
	_, plainSrv := newTestServer(t)
	plainReg := registerClient(t, http.DefaultClient, plainSrv.URL)

	pages := map[string]string{
		"direct TLS":                     consentPageFor(t, tlsSrv.Client(), tlsSrv.URL, tlsReg, "", ""),
		"X-Forwarded-Proto: https":       consentPageFor(t, http.DefaultClient, plainSrv.URL, plainReg, "", "https"),
		"X-Forwarded-Proto: https, http": consentPageFor(t, http.DefaultClient, plainSrv.URL, plainReg, "", "https, http"),
	}
	for name, page := range pages {
		for _, field := range []string{
			`name="imap_insecure"`, `name="imap_skip_verify"`,
			`name="smtp_insecure"`, `name="smtp_skip_verify"`,
		} {
			if strings.Contains(page, field) {
				t.Errorf("%s: consent form must hide %s (local testing only)", name, field)
			}
		}
		for _, field := range []string{
			`name="email"`, `name="password"`,
			`name="imap_host"`, `name="imap_port"`, `name="imap_tls"`, `name="imap_starttls"`,
			`name="smtp_host"`, `name="smtp_port"`, `name="smtp_tls"`, `name="smtp_starttls"`,
			`name="sent_mailbox"`, `name="drafts_mailbox"`,
		} {
			if !strings.Contains(page, field) {
				t.Errorf("%s: consent form missing field %s", name, field)
			}
		}
	}
}

// TestConsentFormShowsInsecureFieldsOverPlainHTTP checks that the "local
// testing only" checkboxes are shown when clients reach the server over
// plain HTTP — regardless of the Host, so a local dev server works on
// localhost, a loopback IP, or a LAN address alike.
func TestConsentFormShowsInsecureFieldsOverPlainHTTP(t *testing.T) {
	oldVerify := VerifyAccount
	VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { VerifyAccount = oldVerify })
	_, srv := newTestServer(t)
	reg := registerClient(t, http.DefaultClient, srv.URL)

	for _, host := range []string{"", "localhost:8080", "127.0.0.1:8080", "192.168.1.10:8080", "devbox.lan"} {
		page := consentPageFor(t, http.DefaultClient, srv.URL, reg, host, "")
		for _, field := range []string{
			`name="imap_insecure"`, `name="imap_skip_verify"`,
			`name="smtp_insecure"`, `name="smtp_skip_verify"`,
		} {
			if !strings.Contains(page, field) {
				t.Errorf("plain-HTTP consent form (host %q) must show %s (local testing only)", host, field)
			}
		}
	}
}

// TestConsentSkipVerifyRoundTrip checks the skip_verify checkboxes survive
// into the account embedded in the issued token.
func TestConsentSkipVerifyRoundTrip(t *testing.T) {
	oldVerify := VerifyAccount
	VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { VerifyAccount = oldVerify })
	s, srv := newTestServer(t)

	resp, err := http.Post(srv.URL+RegisterPath, "application/json",
		strings.NewReader(`{"redirect_uris":["http://localhost/cb"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	json.NewDecoder(resp.Body).Decode(&reg) //nolint:errcheck
	resp.Body.Close()

	verifier := "verifier-123"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	form := url.Values{
		"client_id":             {reg.ClientID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"email":                 {"user@example.org"},
		"password":              {"secret"},
		"imap_host":             {"imap.example.org"},
		"imap_tls":              {"1"},
		"imap_skip_verify":      {"1"},
		"smtp_host":             {"smtp.example.org"},
		"smtp_starttls":         {"1"},
		"smtp_skip_verify":      {"1"},
	}
	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noRedir.PostForm(srv.URL+AuthorizePath, form)
	if err != nil {
		t.Fatal(err)
	}
	code := mustQuery(t, resp.Header.Get("Location"), "code")
	resp.Body.Close()

	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost/cb"},
		"code_verifier": {verifier},
		"client_id":     {reg.ClientID},
		"client_secret": {reg.ClientSecret},
	}
	resp, err = http.PostForm(srv.URL+TokenPath, tokForm)
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tok) //nolint:errcheck
	resp.Body.Close()

	acc, err := s.VerifyAccessToken(context.Background(), tok.AccessToken)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if !acc.IMAP.InsecureSkipVerify {
		t.Errorf("imap skip_verify not carried into token: %+v", acc.IMAP)
	}
	if !acc.SMTP.InsecureSkipVerify {
		t.Errorf("smtp skip_verify not carried into token: %+v", acc.SMTP)
	}
	if acc.IMAP.Port != 993 || !acc.IMAP.TLS {
		t.Errorf("imap tls defaults wrong: %+v", acc.IMAP)
	}
	if acc.SMTP.Port != 587 || acc.SMTP.TLS {
		t.Errorf("smtp starttls defaults wrong: %+v", acc.SMTP)
	}
}
