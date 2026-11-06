package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/config"
)

// startFakeIMAPListener is injected by fakeimap_test.go.
var startFakeIMAP func(t *testing.T) (addr string, cleanup func())

// fakeHeaders returns the X-Email-* headers pointing at the fake IMAP server.
func fakeHeaders(port int) map[string]string {
	return map[string]string{
		config.HeaderAddress:      "user@example.com",
		config.HeaderPassword:     "secret",
		config.HeaderIMAPHost:     "127.0.0.1",
		config.HeaderIMAPPort:     fmt.Sprint(port),
		config.HeaderIMAPInsecure: "1",
		config.HeaderSMTPHost:     "127.0.0.1",
		config.HeaderSMTPPort:     "25",
		config.HeaderSMTPInsecure: "1",
	}
}

// headerTransport injects static headers into every request.
type headerTransport map[string]string

func (h headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range h {
		req.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestDiscoveryEndpoint(t *testing.T) {
	h := New()
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
	if !strings.Contains(s, config.HeaderAddress) {
		t.Fatalf("discovery doc should describe the %s header usage: %s", config.HeaderAddress, s)
	}
}

func TestMissingCredentialReturns401WithChallenge(t *testing.T) {
	h := New()
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
	if !strings.Contains(string(body), "Bearer") {
		t.Fatalf("expected error to mention the Bearer options: %s", body)
	}
}

func TestMalformedAddressReturns400(t *testing.T) {
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Address header present but invalid, plus a password: 400 (bad account
	// description), not 401.
	req, _ := http.NewRequest("POST", srv.URL+MCPEndpoint, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(config.HeaderAddress, "not-an-address")
	req.Header.Set(config.HeaderPassword, "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid address, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), config.HeaderAddress) {
		t.Fatalf("expected error to mention %s: %s", config.HeaderAddress, body)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	h := New()
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

	// Stateless: everything via X-Email-* headers, no server-side config.
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: headerTransport(fakeHeaders(port))},
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

func TestMCPSessionWithElicitation(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	// Address header but no credential: the server must ask via elicitation
	// on the first tool call, then talk to the fake IMAP server.
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, &mcp.ClientOptions{
		ElicitationHandler: func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{
				Action:  "accept",
				Content: map[string]any{"password": "secret"},
			}, nil
		},
	})
	headers := fakeHeaders(port)
	delete(headers, config.HeaderPassword)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: headerTransport(headers)},
	}
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_mailboxes",
		Arguments: map[string]any{"pattern": "*"},
	})
	if err != nil {
		t.Fatalf("call list_mailboxes: %v", err)
	}
	if res.IsError {
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		t.Fatalf("list_mailboxes error: %s", sb.String())
	}
}

func TestMCPSessionViaPasswordHeader(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	// Credential via the custom password header: no elicitation needed.
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil) // no elicitation handler
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: headerTransport(fakeHeaders(port))},
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
	if res.IsError {
		t.Fatalf("expected success with header credential, got error: %+v", res.Content)
	}
}

func TestBearerTokenUsedAsCredential(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	headers := fakeHeaders(port)
	delete(headers, config.HeaderPassword)
	// The fake IMAP server only supports LOGIN, so select the plain
	// mechanism explicitly; a real OAuth2 access token would use xoauth2
	// (the default when only a Bearer token is given).
	headers["Authorization"] = "Bearer secret"
	headers[config.HeaderAuthMech] = "plain"
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: headerTransport(headers)},
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
	if res.IsError {
		t.Fatalf("expected success with bearer credential, got error: %+v", res.Content)
	}
}

func TestWrongHeaderCredentialFails(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	// No elicitation handler: with a wrong header password the login must
	// fail and the error must come from IMAP, not elicitation.
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	headers := fakeHeaders(port)
	headers[config.HeaderPassword] = "wrong-password"
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: headerTransport(headers)},
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
	if !strings.Contains(sb.String(), "authentication failed") && !strings.Contains(sb.String(), "login") {
		t.Fatalf("expected an auth error, got: %s", sb.String())
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"authorization_servers"`) || !strings.Contains(string(body), config.HeaderAddress) {
		t.Fatalf("unexpected metadata: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "/mcp") {
		t.Fatalf("metadata should name the /mcp resource: %s", body)
	}
}

// TestPackedBearerAccount: the whole account in a single Authorization:
// Bearer base64url(JSON) header — the shape clients with only one auth
// header (e.g. Mistral Vibe) use.
func TestPackedBearerAccount(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	acc := &config.Account{
		Email:    "user@example.com",
		Password: "secret",
		IMAP:     config.IMAPConfig{Host: "127.0.0.1", Port: port, Insecure: true},
		SMTP:     config.SMTPConfig{Host: "127.0.0.1", Port: 25, Insecure: true},
	}
	headers := headerTransport{
		"Authorization": "Bearer " + acc.EncodeBearer(),
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + MCPEndpoint,
		HTTPClient: &http.Client{Transport: headers},
	}
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_mailboxes", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success with packed bearer, got: %+v", res.Content)
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

	h := New()
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
		HTTPClient: &http.Client{Transport: headerTransport{"Authorization": "Bearer " + tok.AccessToken}},
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
