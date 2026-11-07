package oauthserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"email-mcp/internal/config"
)

// registerTestClient registers a client and returns its credentials.
func registerTestClient(t *testing.T, srv string) (clientID, clientSecret string) {
	t.Helper()
	resp, err := http.Post(srv+RegisterPath, "application/json",
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
	return reg.ClientID, reg.ClientSecret
}

func consentForm(clientID string) url.Values {
	return url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"http://localhost/cb"},
		"state":                 {"s"},
		"scope":                 {"email"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"email":                 {"user@example.org"},
		"password":              {"secret"},
	}
}

var noRedirClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

// TestConsentVerificationFailure checks that a failing account check makes
// the consent POST re-render the form with an error banner (400) and issue
// no auth code.
func TestConsentVerificationFailure(t *testing.T) {
	_, srv := newTestServer(t)
	clientID, _ := registerTestClient(t, srv.URL)

	oldVerify := VerifyAccount
	VerifyAccount = func(acc *config.Account) error {
		return errors.New("IMAP check failed: IMAP login: authentication failed")
	}
	t.Cleanup(func() { VerifyAccount = oldVerify })

	resp, err := noRedirClient.PostForm(srv.URL+AuthorizePath, consentForm(clientID))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 with error form, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Fatalf("no redirect expected on failure, got %q", loc)
	}
	page := string(body)
	if !strings.Contains(page, "Setup check failed") || !strings.Contains(page, "authentication failed") {
		t.Errorf("error banner missing from re-rendered consent page:\n%s", page)
	}
	// The form must be re-filled (email kept) but never the password.
	if !strings.Contains(page, `value="user@example.org"`) {
		t.Error("email not pre-filled after failure")
	}
	if strings.Contains(page, `value="secret"`) {
		t.Error("password must never be re-rendered")
	}
}

// TestConsentVerificationSuccess checks the happy path still issues a code
// when verification passes.
func TestConsentVerificationSuccess(t *testing.T) {
	_, srv := newTestServer(t)
	clientID, _ := registerTestClient(t, srv.URL)

	oldVerify := VerifyAccount
	var got *config.Account
	VerifyAccount = func(acc *config.Account) error { got = acc; return nil }
	t.Cleanup(func() { VerifyAccount = oldVerify })

	resp, err := noRedirClient.PostForm(srv.URL+AuthorizePath, consentForm(clientID))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 with code, got %d", resp.StatusCode)
	}
	u, _ := url.Parse(resp.Header.Get("Location"))
	if u.Query().Get("code") == "" {
		t.Fatalf("no code in redirect: %s", u)
	}
	if got == nil || got.Email != "user@example.org" || got.Password != "secret" {
		t.Errorf("verifyAccount received wrong account: %+v", got)
	}
}
