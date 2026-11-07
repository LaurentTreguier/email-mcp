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

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(&Options{BaseURL: "https://mcp.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv
}

func TestMetadataAndJWKS(t *testing.T) {
	oldVerify := VerifyAccount
	VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { VerifyAccount = oldVerify })
	_, srv := newTestServer(t)
	for path, want := range map[string]string{
		MetadataPath: `"issuer":"https://mcp.example.org"`,
		JWKSPath:     `"kty":"RSA"`,
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), want) {
			t.Errorf("GET %s: %d %s", path, resp.StatusCode, body)
		}
	}
}

func TestFullFlowAndVerify(t *testing.T) {
	oldVerify := VerifyAccount
	VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { VerifyAccount = oldVerify })
	s, srv := newTestServer(t)

	// Register a client.
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
	if reg.ClientID == "" {
		t.Fatal("no client_id")
	}

	// Consent POST → redirect with code.
	form := url.Values{
		"client_id":             {reg.ClientID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"email":                 {"user@example.org"},
		"password":              {"secret"},
	}
	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noRedir.PostForm(srv.URL+AuthorizePath, form)
	if err != nil {
		t.Fatal(err)
	}
	loc := resp.Header.Get("Location")
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	code := mustQuery(t, loc, "code")

	// Bad verifier must fail PKCE.
	bad := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {"wrong"}, "client_id": {reg.ClientID}, "client_secret": {reg.ClientSecret},
	}
	resp, err = http.PostForm(srv.URL+TokenPath, bad)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 200 || !strings.Contains(string(body), "invalid_grant") {
		t.Fatalf("expected invalid_grant for bad PKCE verifier, got %d %s", resp.StatusCode, body)
	}

	// Re-do consent for a fresh code (the old one is consumed/failed).
	resp, err = noRedir.PostForm(srv.URL+AuthorizePath, form)
	if err != nil {
		t.Fatal(err)
	}
	code = mustQuery(t, resp.Header.Get("Location"), "code")
	resp.Body.Close()

	// Good verifier: S256("test-verifier") — compute it.
	verifier := "test-verifier"
	sum := sha256.Sum256([]byte(verifier))
	good := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier},
		"redirect_uri":  {"http://localhost/cb"},
		"client_id":     {reg.ClientID},
		"client_secret": {reg.ClientSecret},
	}
	_ = sum
	// patch the challenge: consent used "challenge"; we need the real S256 of verifier,
	// so redo consent with the correct challenge.
	ch := base64.RawURLEncoding.EncodeToString(sum[:])
	form.Set("code_challenge", ch)
	resp, err = noRedir.PostForm(srv.URL+AuthorizePath, form)
	if err != nil {
		t.Fatal(err)
	}
	code = mustQuery(t, resp.Header.Get("Location"), "code")
	resp.Body.Close()
	good.Set("code", code)

	resp, err = http.PostForm(srv.URL+TokenPath, good)
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tok) //nolint:errcheck
	resp.Body.Close()
	if tok.AccessToken == "" {
		t.Fatal("no access token")
	}

	// The server can verify its own token and recover the account.
	acc, err := s.VerifyAccessToken(context.Background(), tok.AccessToken)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if acc.Email != "user@example.org" || acc.Password != "secret" {
		t.Errorf("recovered account = %+v", acc)
	}

	// Garbage token is rejected.
	if _, err := s.VerifyAccessToken(context.Background(), "garbage.token.here"); err == nil {
		t.Error("expected error for garbage token")
	}
}

func mustQuery(t *testing.T, loc, key string) string {
	t.Helper()
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	v := u.Query().Get(key)
	if v == "" {
		t.Fatalf("missing %s in %s", key, loc)
	}
	return v
}
