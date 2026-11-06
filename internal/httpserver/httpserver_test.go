package httpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestMissingAddressReturns400(t *testing.T) {
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	// POST an initialize request without the X-Email-Address header; expect a 400.
	req, _ := http.NewRequest("POST", srv.URL+MCPEndpoint, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing address header, got %d", resp.StatusCode)
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

	// No credential header: the server must ask via elicitation on the
	// first tool call, then talk to the fake IMAP server.
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
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"custom_headers"`) || !strings.Contains(string(body), config.HeaderAddress) {
		t.Fatalf("unexpected metadata: %d %s", resp.StatusCode, body)
	}
}

func splitAddr(addr string) (string, int) {
	host, portStr, _ := strings.Cut(addr, ":")
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
