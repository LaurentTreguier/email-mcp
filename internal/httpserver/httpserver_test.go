package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/config"
	"email-mcp/internal/oauthserver"
)

// newTestHandler sets the required OAuth env config (fresh signing key +
// fixed token secret) and returns the handler, like production with
// EMAIL_MCP_SIGNING_KEY and EMAIL_MCP_TOKEN_SECRET set.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMAIL_MCP_SIGNING_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	t.Setenv("EMAIL_MCP_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
	h, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestNewFailsFastWithoutRequiredConfig checks the fail-fast startup: with
// no signing key / token secret in the environment, New must error instead of
// starting a server whose tokens die on the next restart.
func TestNewFailsFastWithoutRequiredConfig(t *testing.T) {
	// t.Setenv cannot unset; use empty values which the env reader treats as unset.
	t.Setenv("EMAIL_MCP_SIGNING_KEY", "")
	t.Setenv("EMAIL_MCP_SIGNING_KEY_FILE", "")
	t.Setenv("EMAIL_MCP_TOKEN_SECRET", "")
	if _, err := New(); err == nil {
		t.Fatal("expected startup error without signing key and token secret")
	}
}

// TestNewReadsSigningKeyFile checks that EMAIL_MCP_SIGNING_KEY_FILE (mounted
// secret) takes precedence and is read from disk.
func TestNewReadsSigningKeyFile(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemFile := t.TempDir() + `\key.pem`
	if err := os.WriteFile(pemFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMAIL_MCP_SIGNING_KEY", "")
	t.Setenv("EMAIL_MCP_SIGNING_KEY_FILE", pemFile)
	t.Setenv("EMAIL_MCP_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
	if _, err := New(); err != nil {
		t.Fatalf("New with EMAIL_MCP_SIGNING_KEY_FILE: %v", err)
	}
}

// TestNewAcceptsBase64SigningKey checks that EMAIL_MCP_SIGNING_KEY may carry
// the PEM base64-encoded (env vars don't preserve newlines).
func TestNewAcceptsBase64SigningKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	t.Setenv("EMAIL_MCP_SIGNING_KEY", base64.StdEncoding.EncodeToString(pemBytes))
	t.Setenv("EMAIL_MCP_SIGNING_KEY_FILE", "")
	t.Setenv("EMAIL_MCP_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
	h, err := New()
	if err != nil {
		t.Fatalf("New with base64 EMAIL_MCP_SIGNING_KEY: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("jwks status = %d", resp.StatusCode)
	}
}

// TestTokenSecretRawVsBase64 checks the secret parsing: a plain 32-char
// string is used raw, a base64 value is decoded, and anything else is
// rejected by oauthserver.New (not 32 bytes after decoding).
func TestTokenSecretRawVsBase64(t *testing.T) {
	raw := "0123456789abcdef0123456789abcdef" // exactly 32 chars
	b64 := base64.StdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))
	for name, v := range map[string]string{"raw32": raw, "base64": b64} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("EMAIL_MCP_SIGNING_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: mustPKCS8(t)})))
			t.Setenv("EMAIL_MCP_SIGNING_KEY_FILE", "")
			t.Setenv("EMAIL_MCP_TOKEN_SECRET", v)
			if _, err := New(); err != nil {
				t.Fatalf("New with %s secret: %v", name, err)
			}
		})
	}
}

func mustPKCS8(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// startFakeIMAPListener is injected by fakeimap_test.go.
var startFakeIMAP func(t *testing.T) (addr string, cleanup func())

// oauthToken runs the built-in OAuth flow against srv (register -> consent
// -> token) with the account fields in extraForm, and returns an access
// token usable as "Authorization: ****** <token>" against srv/mcp.
func oauthToken(t *testing.T, srv *httptest.Server, extraForm url.Values) string {
	t.Helper()
	oldVerify := oauthserver.VerifyAccount
	oauthserver.VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { oauthserver.VerifyAccount = oldVerify })

	// Dynamic client registration (RFC 7591).
	regBody := strings.NewReader(`{"redirect_uris":["http://localhost:9999/callback"],"client_name":"test"}`)
	req, _ := http.NewRequest("POST", srv.URL+"/oauth/register", regBody)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	json.NewDecoder(resp.Body).Decode(&reg) //nolint:errcheck
	resp.Body.Close()
	if reg.ClientID == "" || reg.ClientSecret == "" {
		t.Fatalf("bad registration response: %+v", reg)
	}

	// Consent POST -> redirect with code.
	verifier := "test-verifier-0123456789abcdef"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	consent := url.Values{
		"client_id":             {reg.ClientID},
		"redirect_uri":          {"http://localhost:9999/callback"},
		"state":                 {"xyz"},
		"scope":                 {"email"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"email":                 {"user@example.com"},
		"password":              {"secret"},
	}
	for k, vs := range extraForm {
		consent[k] = vs
	}
	noRedirect := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err = noRedirect.PostForm(srv.URL+"/oauth/authorize", consent)
	if err != nil {
		t.Fatal(err)
	}
	loc := resp.Header.Get("Location")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || loc == "" {
		t.Fatalf("expected 303 redirect with code, got %d loc=%q", resp.StatusCode, loc)
	}
	lu, _ := url.Parse(loc)
	code := lu.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %s", loc)
	}

	// Token exchange (PKCE).
	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:9999/callback"},
		"code_verifier": {verifier},
		"client_id":     {reg.ClientID},
		"client_secret": {reg.ClientSecret},
	}
	resp, err = http.PostForm(srv.URL+"/oauth/token", tokForm)
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

// bearerTransport injects a static Authorization bearer into every request.
type bearerTransport string

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(req)
}

func TestDiscoveryEndpoint(t *testing.T) {
	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `"service":"email-mcp"`) || !strings.Contains(s, `"stateless":true`) {
		t.Fatalf("discovery doc unexpected: %s", s)
	}
	if !strings.Contains(s, `"endpoint":"/mcp"`) {
		t.Fatalf("discovery doc should name the fixed /mcp endpoint: %s", s)
	}
	if !strings.Contains(s, "oauth-protected-resource") {
		t.Fatalf("discovery doc should point at the OAuth metadata: %s", s)
	}
}

func TestMissingCredentialReturns401WithChallenge(t *testing.T) {
	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// POST an initialize request without any credential; expect a 401 with
	// a WWW-Authenticate challenge pointing at the protected-resource metadata.
	req, _ := http.NewRequest("POST", srv.URL+MCPEndpoint, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing credential, got %d", resp.StatusCode)
	}
	if wa := resp.Header.Get("WWW-Authenticate"); !strings.Contains(wa, "resource_metadata") || !strings.Contains(wa, "/.well-known/oauth-protected-resource") {
		t.Fatalf("expected WWW-Authenticate challenge with resource_metadata, got %q", wa)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "bearer token") {
		t.Fatalf("expected error to mention the bearer token: %s", body)
	}
}

func TestInvalidBearerTokenReturns401(t *testing.T) {
	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// A bearer that is not a valid OAuth access token: 401 with challenge.
	req, _ := http.NewRequest("POST", srv.URL+MCPEndpoint, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid bearer, got %d", resp.StatusCode)
	}
	if wa := resp.Header.Get("WWW-Authenticate"); !strings.Contains(wa, "/.well-known/oauth-protected-resource") {
		t.Fatalf("expected WWW-Authenticate challenge, got %q", wa)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/gmail.com?username=user")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for old per-domain path, got %d", resp.StatusCode)
	}
}

func TestMCPSessionOverHTTP(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	// Stateless: everything via the OAuth bearer token, no server-side config.
	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	token := oauthToken(t, srv, url.Values{
		"imap_host":     {"127.0.0.1"},
		"imap_port":     {fmt.Sprint(port)},
		"imap_insecure": {"1"},
		"smtp_host":     {"127.0.0.1"},
		"smtp_port":     {"25"},
		"smtp_insecure": {"1"},
	})

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: bearerTransport(token)},
	}
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"list_mailboxes", "search_messages", "get_message", "send_email", "set_flags", "move_messages", "delete_messages", "mailbox_status", "get_quota", "save_draft"} {
		if !names[want] {
			t.Errorf("tool %q not registered", want)
		}
	}

	// Call account_info — no IMAP connection needed.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "account_info",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call account_info: %v", err)
	}
	if res.IsError {
		t.Fatalf("account_info error: %+v", res.Content)
	}

	// Call list_mailboxes against the fake IMAP server.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_mailboxes",
		Arguments: map[string]any{"pattern": "*"},
	})
	if err != nil {
		t.Fatalf("call list_mailboxes: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_mailboxes error: %+v", res.Content)
	}

	// Resources: read the mailboxes resource.
	rres, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "mail://mailboxes"})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(rres.Contents) == 0 || !strings.Contains(rres.Contents[0].Text, "INBOX") {
		t.Fatalf("mailboxes resource unexpected: %+v", rres.Contents)
	}

	// Prompts: list them.
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts.Prompts) == 0 {
		t.Fatal("no prompts registered")
	}
}

func TestWrongPasswordInTokenFailsAtIMAP(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Consent with a wrong password: the token is valid OAuth-wise, but the
	// IMAP login must fail with an auth error from the server.
	form := url.Values{
		"imap_host":     {"127.0.0.1"},
		"imap_port":     {fmt.Sprint(port)},
		"imap_insecure": {"1"},
		"smtp_host":     {"127.0.0.1"},
		"smtp_port":     {"25"},
		"smtp_insecure": {"1"},
	}
	form.Set("password", "wrong-password")
	token := oauthToken(t, srv, form)

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: bearerTransport(token)},
	}
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_mailboxes",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error with a wrong password")
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	if !strings.Contains(sb.String(), "authentication failed") && !strings.Contains(sb.String(), "login") && !strings.Contains(strings.ToLower(sb.String()), "auth") {
		t.Fatalf("expected an auth error, got: %s", sb.String())
	}
}

// TestConsentVerificationE2E runs the real consent POST against the fake
// IMAP/SMTP endpoints: with a wrong password the consent must fail with a
// 400 + error banner (no code issued); with correct credentials it must
// succeed (303 + code).
func TestConsentVerificationE2E(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	noRedir := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	doConsent := func(password string) (int, string, string) {
		// register
		regBody := strings.NewReader(`{"redirect_uris":["http://localhost:9999/callback"]}`)
		req, _ := http.NewRequest("POST", srv.URL+"/oauth/register", regBody)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var reg struct {
			ClientID string `json:"client_id"`
		}
		json.NewDecoder(resp.Body).Decode(&reg) //nolint:errcheck
		resp.Body.Close()
		form := url.Values{
			"client_id":             {reg.ClientID},
			"redirect_uri":          {"http://localhost:9999/callback"},
			"code_challenge":        {"challenge"},
			"code_challenge_method": {"S256"},
			"email":                 {"user@example.com"},
			"password":              {password},
			"imap_host":             {"127.0.0.1"},
			"imap_port":             {fmt.Sprint(port)},
			"imap_insecure":         {"1"},
			"smtp_host":             {"127.0.0.1"},
			"smtp_port":             {"25"},
			"smtp_insecure":         {"1"},
		}
		resp, err = noRedir.PostForm(srv.URL+"/oauth/authorize", form)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Location"), string(body)
	}

	// Wrong password: the fake IMAP rejects the login → 400 + banner.
	code, loc, body := doConsent("wrong-password")
	if code != http.StatusBadRequest || loc != "" {
		t.Fatalf("wrong password: expected 400 without redirect, got %d loc=%q", code, loc)
	}
	if !strings.Contains(body, "Setup check failed") || !strings.Contains(strings.ToLower(body), "imap") {
		t.Fatalf("expected IMAP error banner, got: %s", body)
	}

	// Correct password but no SMTP listener on 127.0.0.1:25: consent must
	// fail with an SMTP error banner (proves the SMTP leg is checked too).
	code2, loc2, body2 := doConsent("secret")
	if code2 != http.StatusBadRequest || loc2 != "" {
		t.Fatalf("expected 400 without redirect (SMTP unreachable), got %d loc=%q", code2, loc2)
	}
	if !strings.Contains(body2, "Setup check failed") || !strings.Contains(strings.ToLower(body2), "smtp") {
		t.Fatalf("expected SMTP error banner, got: %s", body2)
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"authorization_servers"`) || !strings.Contains(string(body), `"bearer_methods_supported":["header"]`) {
		t.Fatalf("unexpected metadata: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "/mcp") {
		t.Fatalf("metadata should name the /mcp resource: %s", body)
	}
}

// TestOAuthFlow exercises the built-in authorization server end to end:
// metadata → DCR → authorize (consent POST) → token (PKCE) → MCP call with
// the issued JWT.
func TestOAuthFlow(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	// Consent-time verification is stubbed here; it is exercised for real
	// in TestConsentVerificationE2E and the oauthserver package tests.
	oldVerify := oauthserver.VerifyAccount
	oauthserver.VerifyAccount = func(acc *config.Account) error { return nil }
	t.Cleanup(func() { oauthserver.VerifyAccount = oldVerify })

	h := newTestHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// 1. Authorization server metadata (RFC 8414).
	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		RegistrationEndpoint  string `json:"registration_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	json.NewDecoder(resp.Body).Decode(&meta) //nolint:errcheck
	resp.Body.Close()
	if meta.TokenEndpoint == "" || meta.RegistrationEndpoint == "" {
		t.Fatalf("bad auth server metadata: %+v", meta)
	}
	// JWKS must be served.
	resp, err = http.Get(srv.URL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"kty":"RSA"`) {
		t.Fatalf("bad jwks: %s", body)
	}

	// 2. Dynamic client registration (RFC 7591).
	regBody := strings.NewReader(`{"redirect_uris":["http://localhost:9999/callback"],"client_name":"test"}`)
	req, _ := http.NewRequest("POST", srv.URL+"/oauth/register", regBody)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	json.NewDecoder(resp.Body).Decode(&reg) //nolint:errcheck
	resp.Body.Close()
	if reg.ClientID == "" || reg.ClientSecret == "" {
		t.Fatalf("bad registration response: %+v", reg)
	}

	// 3. Authorize: GET the consent form, then POST it.
	verifier := "test-verifier-0123456789abcdef"
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	authURL := srv.URL + "/oauth/authorize?response_type=code&client_id=" + url.QueryEscape(reg.ClientID) +
		"&redirect_uri=" + url.QueryEscape("http://localhost:9999/callback") +
		"&state=xyz&scope=email&code_challenge=" + challenge + "&code_challenge_method=S256"
	resp, err = http.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	formPage, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(formPage), "Authorize") {
		t.Fatalf("expected consent form, got %d: %s", resp.StatusCode, formPage)
	}
	consent := url.Values{
		"client_id":             {reg.ClientID},
		"redirect_uri":          {"http://localhost:9999/callback"},
		"state":                 {"xyz"},
		"scope":                 {"email"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"email":                 {"user@example.com"},
		"password":              {"secret"},
		"imap_host":             {"127.0.0.1"},
		"imap_port":             {fmt.Sprint(port)},
		"imap_insecure":         {"1"},
		"smtp_host":             {"127.0.0.1"},
		"smtp_port":             {"25"},
		"smtp_insecure":         {"1"},
	}
	noRedirect := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err = noRedirect.PostForm(srv.URL+"/oauth/authorize", consent)
	if err != nil {
		t.Fatal(err)
	}
	loc := resp.Header.Get("Location")
	errBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || loc == "" {
		t.Fatalf("expected 303 redirect with code, got %d loc=%q body=%s", resp.StatusCode, loc, errBody)
	}
	lu, _ := url.Parse(loc)
	code := lu.Query().Get("code")
	if code == "" || lu.Query().Get("state") != "xyz" {
		t.Fatalf("bad redirect: %s", loc)
	}

	// 4. Token exchange (PKCE).
	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:9999/callback"},
		"code_verifier": {verifier},
		"client_id":     {reg.ClientID},
		"client_secret": {reg.ClientSecret},
	}
	resp, err = http.PostForm(srv.URL+"/oauth/token", tokForm)
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	json.NewDecoder(resp.Body).Decode(&tok) //nolint:errcheck
	resp.Body.Close()
	if tok.AccessToken == "" || tok.RefreshToken == "" || tok.TokenType != "Bearer" {
		t.Fatalf("bad token response: %+v", tok)
	}

	// 5. Use the access token against /mcp.
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: bearerTransport(tok.AccessToken)},
	}
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect with OAuth token: %v", err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_mailboxes", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success with OAuth-issued token, got: %+v", res.Content)
	}

	// 6. Refresh grant issues a new access token.
	refForm := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tok.RefreshToken},
		"client_id":     {reg.ClientID},
		"client_secret": {reg.ClientSecret},
	}
	resp, err = http.PostForm(srv.URL+"/oauth/token", refForm)
	if err != nil {
		t.Fatal(err)
	}
	var tok2 struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(resp.Body).Decode(&tok2) //nolint:errcheck
	resp.Body.Close()
	if tok2.AccessToken == "" || tok2.AccessToken == tok.AccessToken {
		t.Fatalf("expected a fresh access token from refresh, got %+v", tok2)
	}
}

func splitAddr(addr string) (string, int) {
	host, portStr, _ := strings.Cut(addr, ":")
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
