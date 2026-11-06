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
)

// startFakeIMAPListener is injected by fakeimap_test.go.
var startFakeIMAP func(t *testing.T) (addr string, cleanup func())

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
	if !strings.Contains(s, "username=user") {
		t.Fatalf("discovery doc should describe /{domain}?username=... usage: %s", s)
	}
}

func TestMissingUsernameReturns400(t *testing.T) {
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	// POST an initialize request without the username param; expect a 400.
	req, _ := http.NewRequest("POST", srv.URL+"/unknown.net", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing username, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "username") {
		t.Fatalf("expected error to mention username: %s", body)
	}
}

func TestMCPSessionOverHTTP(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	// Stateless: everything via query params, no server-side config.
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: srv.URL + "/example.com?username=user&imap_host=127.0.0.1&imap_port=0&smtp_host=127.0.0.1&smtp_port=25&imap_insecure=1&smtp_insecure=1"}
	transport.Endpoint = fmt.Sprintf("%s/example.com?username=user&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
	transport.HTTPClient = &http.Client{Transport: headerRoundTripper{header: HeaderEmailPassword, value: "secret"}}
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

func TestMCPSessionViaQueryParamsWithElicitation(t *testing.T) {
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
	endpoint := fmt.Sprintf("%s/example.com?username=user&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
	transport := &mcp.StreamableClientTransport{Endpoint: endpoint}
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

func TestMCPSessionViaHeaderCredentials(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	// Credential via custom header: no elicitation needed.
	h := New()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil) // no elicitation handler
	endpoint := fmt.Sprintf("%s/example.com?username=user&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
	transport := &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: headerRoundTripper{header: HeaderEmailPassword, value: "secret"},
		},
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
	endpoint := fmt.Sprintf("%s/example.com?username=user&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
	transport := &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: headerRoundTripper{header: "Authorization", value: "Bearer secret"},
		},
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
		t.Fatalf("expected success with Bearer credential, got error: %+v", res.Content)
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
	endpoint := fmt.Sprintf("%s/example.com?username=user&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
	transport := &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: headerRoundTripper{header: HeaderEmailPassword, value: "wrong-password"},
		},
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
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"custom_header":"X-Email-Password"`) {
		t.Fatalf("unexpected metadata: %d %s", resp.StatusCode, body)
	}
}

// headerRoundTripper injects a static header into every request.
type headerRoundTripper struct {
	header, value string
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set(h.header, h.value)
	return http.DefaultTransport.RoundTrip(req)
}

func splitAddr(addr string) (string, int) {
	host, portStr, _ := strings.Cut(addr, ":")
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
