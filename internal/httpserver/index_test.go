package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, h http.Handler, path, accept string) (int, string, http.Header) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

func TestRootHowToPageForBrowser(t *testing.T) {
	h := newTestHandler(t)
	code, body, hdr := get(t, h, "/", "text/html,application/xhtml+xml")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	// The page explains the OAuth-only flow and the fixed endpoint; it must
	// not offer the old X-Email-* header form anymore.
	for _, want := range []string{MCPEndpoint, "/oauth/authorize", "/oauth/register", "/oauth/token", "oauth-protected-resource"} {
		if !strings.Contains(body, want) {
			t.Errorf("how-to page missing %q", want)
		}
	}
	if strings.Contains(body, "X-Email-") {
		t.Error("how-to page must not mention the removed X-Email-* headers")
	}
	if strings.Contains(body, "<form") {
		t.Error("how-to page must not contain a setup form")
	}
}

func TestRootJSONForClients(t *testing.T) {
	h := newTestHandler(t)
	code, body, hdr := get(t, h, "/", "application/json")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !strings.Contains(body, `"endpoint":"/mcp"`) || !strings.Contains(body, "oauth-protected-resource") {
		t.Fatalf("discovery doc unexpected: %s", body)
	}
	if strings.Contains(body, "X-Email-") {
		t.Error("discovery doc must not mention the removed X-Email-* headers")
	}
}
