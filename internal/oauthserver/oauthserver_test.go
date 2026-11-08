package oauthserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"email-mcp/internal/config"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// testSigningKeyPEM returns a freshly generated RSA key in PKCS#8 PEM form.
func testSigningKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// testOptions returns Options with a generated signing key and fixed secret.
func testOptions(t *testing.T) *Options {
	t.Helper()
	return &Options{
		BaseURL:       "https://mcp.example.org",
		SigningKeyPEM: testSigningKeyPEM(t),
		TokenSecret:   []byte("0123456789abcdef0123456789abcdef"),
	}
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(testOptions(t))
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

// issueTestToken runs register + consent + token exchange against srv and
// returns an access token.
func issueTestToken(t *testing.T, srv string) string {
	t.Helper()
	oldVerify := VerifyAccount
	VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { VerifyAccount = oldVerify })

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

	verifier := "restart-verifier"
	sum := sha256.Sum256([]byte(verifier))
	form := url.Values{
		"client_id":             {reg.ClientID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"email":                 {"user@example.org"},
		"password":              {"secret"},
	}
	noRedir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noRedir.PostForm(srv+AuthorizePath, form)
	if err != nil {
		t.Fatal(err)
	}
	code := mustQuery(t, resp.Header.Get("Location"), "code")
	resp.Body.Close()

	resp, err = http.PostForm(srv+TokenPath, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost/cb"},
		"code_verifier": {verifier},
		"client_id":     {reg.ClientID},
		"client_secret": {reg.ClientSecret},
	})
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tok) //nolint:errcheck
	resp.Body.Close()
	if tok.AccessToken == "" {
		t.Fatal("no access token issued")
	}
	return tok.AccessToken
}

// TestTokenSurvivesRestart proves that with a configured signing key and
// token secret, an access token issued before a "restart" (a second New with
// the same PEM/secret) still verifies afterwards — same kid, same AEAD key.
func TestTokenSurvivesRestart(t *testing.T) {
	opts := testOptions(t)
	s1, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv1 := httptest.NewServer(s1.Handler())
	defer srv1.Close()
	token := issueTestToken(t, srv1.URL)
	srv1.Close()

	// Simulated restart: new process, same configured key + secret.
	s2, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if s2.keyID != s1.keyID {
		t.Fatalf("kid changed across restart: %s vs %s", s2.keyID, s1.keyID)
	}
	acc, err := s2.VerifyAccessToken(context.Background(), token)
	if err != nil {
		t.Fatalf("token issued before restart rejected after restart: %v", err)
	}
	if acc.Email != "user@example.org" {
		t.Errorf("recovered account = %+v", acc)
	}
}

func TestNewRequiresSigningKey(t *testing.T) {
	if _, err := New(&Options{TokenSecret: []byte("0123456789abcdef0123456789abcdef")}); err == nil {
		t.Fatal("expected error when SigningKeyPEM is missing")
	}
	if _, err := New(&Options{SigningKeyPEM: []byte("not pem")}); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestNewRequiresTokenSecret(t *testing.T) {
	if _, err := New(&Options{SigningKeyPEM: testSigningKeyPEM(t)}); err == nil {
		t.Fatal("expected error when TokenSecret is missing")
	}
	if _, err := New(&Options{SigningKeyPEM: testSigningKeyPEM(t), TokenSecret: []byte("short")}); err == nil {
		t.Fatal("expected error when TokenSecret is not 32 bytes")
	}
}

// TestParseSigningKeyPEM accepts both PKCS#8 and PKCS#1 PEM encodings.
func TestParseSigningKeyPEM(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: func() []byte {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}()})
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	for name, p := range map[string][]byte{"pkcs8": pkcs8, "pkcs1": pkcs1} {
		got, err := ParseSigningKeyPEM(p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.N.Cmp(key.N) != 0 {
			t.Errorf("%s: wrong key", name)
		}
	}
}
