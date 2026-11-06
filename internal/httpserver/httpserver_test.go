package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/config"
)

// startFakeIMAPListener is injected by the test to avoid an import cycle with
// the mail package's test helpers; see TestMain-style wiring below.
var startFakeIMAP func(t *testing.T) (addr string, cleanup func())

func TestMain(m *testing.M) {
	// The fake IMAP server lives in internal/mail's test package; to avoid a
	// dependency cycle we run a tiny inline IMAP greeting/login server here.
	m.Run()
}

func TestDiscoveryEndpoint(t *testing.T) {
	accs := &config.Accounts{Accounts: []config.Account{
		{Email: "user@gmail.com", Password: "x", IMAP: config.IMAPConfig{Host: "127.0.0.1", Port: 1}, SMTP: config.SMTPConfig{Host: "127.0.0.1", Port: 1}},
		{Email: "other@example.org", Password: "x", IMAP: config.IMAPConfig{Host: "127.0.0.1", Port: 1}, SMTP: config.SMTPConfig{Host: "127.0.0.1", Port: 1}},
	}}
	h := New(accs.ByDomain())
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, `"/gmail.com"`) || !strings.Contains(body, `"/example.org"`) {
		t.Fatalf("discovery doc missing accounts: %s", body)
	}
}

func TestUnknownDomainReturnsError(t *testing.T) {
	accs := &config.Accounts{Accounts: []config.Account{
		{Email: "user@gmail.com", Password: "x", IMAP: config.IMAPConfig{Host: "127.0.0.1", Port: 1}, SMTP: config.SMTPConfig{Host: "127.0.0.1", Port: 1}},
	}}
	h := New(accs.ByDomain())
	srv := httptest.NewServer(h)
	defer srv.Close()

	// POST an initialize request to an unknown domain; expect a 4xx.
	req, _ := http.NewRequest("POST", srv.URL+"/unknown.net", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Fatalf("expected 4xx for unknown domain, got %d", resp.StatusCode)
	}
}

func TestMCPSessionOverHTTP(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	acc := &config.Account{
		Email:    "user@example.com",
		Username: "user@example.com",
		Password: "secret",
		IMAP:     config.IMAPConfig{Host: "127.0.0.1", Port: port, Insecure: true},
		SMTP:     config.SMTPConfig{Host: "127.0.0.1", Port: 1, Insecure: true},
	}
	acc.Normalize()
	h := New(map[string]*config.Account{"example.com": acc})
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: srv.URL + "/example.com"}
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

func TestMCPSessionViaQueryParams(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	// 0-config: no static accounts at all; everything via query params,
	// pointing at the fake in-memory IMAP server.
	h := New(map[string]*config.Account{})
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
	endpoint := fmt.Sprintf("%s/example.com?email=user@example.com&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
	transport := &mcp.StreamableClientTransport{Endpoint: endpoint}
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	// list_mailboxes should trigger elicitation (password "secret") and then
	// talk to the fake IMAP server.
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

	// 0-config + credential via custom header: no elicitation needed.
	h := New(map[string]*config.Account{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil) // no elicitation handler
	endpoint := fmt.Sprintf("%s/example.com?email=user@example.com&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
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

	// Without the credential header this must fail (no elicitation support);
	// with the header, list_mailboxes must succeed against the fake IMAP.
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

func TestBearerTokenUsedAsCredential(t *testing.T) {
	if startFakeIMAP == nil {
		t.Skip("no fake IMAP server wired")
	}
	addr, cleanup := startFakeIMAP(t)
	defer cleanup()
	_, port := splitAddr(addr)

	h := New(map[string]*config.Account{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	endpoint := fmt.Sprintf("%s/example.com?email=user@example.com&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
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

	h := New(map[string]*config.Account{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	// No elicitation handler: with a wrong header password the login must
	// fail and the error must come from IMAP, not elicitation.
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	endpoint := fmt.Sprintf("%s/example.com?email=user@example.com&imap_host=127.0.0.1&imap_port=%d&imap_insecure=1&smtp_host=127.0.0.1&smtp_port=25&smtp_insecure=1", srv.URL, port)
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
	h := New(map[string]*config.Account{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if resp.StatusCode != 200 || !strings.Contains(body, `"custom_header":"X-Email-Password"`) {
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

var _ = net.Listen
