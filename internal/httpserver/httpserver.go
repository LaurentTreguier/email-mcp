// Package httpserver exposes mail accounts over MCP Streamable HTTP.
//
// Each configured account is served at /{domain}, where domain is the domain
// part of the account's email address, e.g. https://mcp.example.org/gmail.com.
package httpserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/config"
	"email-mcp/internal/mcpserver"
)

// New returns an http.Handler routing /{domain} to the MCP Streamable HTTP
// endpoint of the matching account. The root path returns a JSON discovery
// document listing the available account paths.
func New(byDomain map[string]*config.Account) http.Handler {
	var mu sync.Mutex
	servers := map[string]*mcp.Server{}
	stopCh := make(chan struct{})

	getServer := func(r *http.Request) *mcp.Server {
		domain := strings.Trim(r.URL.Path, "/")
		if domain == "" || strings.Contains(domain, "/") {
			return nil
		}
		domain = strings.ToLower(domain)
		acc, ok := byDomain[domain]
		if !ok {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		s, ok := servers[domain]
		if !ok {
			s = mcpserver.New(acc)
			if sess := mcpserver.SessionOf(s); sess != nil {
				go sess.KeepAlive(stopCh)
			}
			servers[domain] = s
		}
		return s
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Logger: slog.Default(),
	})

	authToken := os.Getenv("EMAIL_MCP_AUTH_TOKEN")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authToken != "" {
			const prefix = "Bearer "
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, prefix) || strings.TrimPrefix(h, prefix) != authToken {
				w.Header().Set("WWW-Authenticate", `Bearer realm="email-mcp"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if strings.Trim(r.URL.Path, "/") == "" {
			w.Header().Set("Content-Type", "application/json")
			domains := make([]string, 0, len(byDomain))
			for domain := range byDomain {
				domains = append(domains, domain)
			}
			fmt.Fprintf(w, `{"service":"email-mcp","accounts":[`)
			for i, d := range domains {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `"/%s"`, d)
			}
			fmt.Fprint(w, `]}`)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
}
