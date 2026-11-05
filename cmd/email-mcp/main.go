// Command email-mcp serves mail accounts over MCP Streamable HTTP.
//
// Each configured account is exposed at https://<host>/<domain>, where <domain>
// is the domain of the account's email address, e.g. https://mcp.example.org/gmail.com
// for user@gmail.com.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"email-mcp/internal/config"
	"email-mcp/internal/httpserver"
)

func main() {
	addr := flag.String("addr", envOr("EMAIL_MCP_ADDR", ":8080"), "listen address for the Streamable HTTP server")
	configPath := flag.String("config", envOr("EMAIL_MCP_CONFIG", ""), "path to accounts JSON file (default ~/.config/email-mcp/accounts.json; EMAIL_MCP_ACCOUNTS env var takes precedence)")
	flag.Parse()

	accs, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}
	byDomain := accs.ByDomain()

	log.Printf("email-mcp: serving %d account(s):", len(byDomain))
	for domain, acc := range byDomain {
		log.Printf("  /%s  ->  %s", domain, acc.Email)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpserver.New(byDomain),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx) //nolint:errcheck
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
