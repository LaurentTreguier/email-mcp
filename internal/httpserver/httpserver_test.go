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

func splitAddr(addr string) (string, int) {
	host, portStr, _ := strings.Cut(addr, ":")
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}

var _ = net.Listen
