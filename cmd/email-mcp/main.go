// Command email-mcp serves mail accounts over MCP Streamable HTTP.
//
// Zero-config mode (default): any domain path is accepted, e.g.
//
//	https://mcp.example.org/gmail.com?email=user@gmail.com
//
// IMAP/SMTP endpoints are auto-discovered via DNS SRV/MX records (or given
// explicitly as query params: imap_host, imap_port, imap_tls, smtp_host, ...),
// and the password is requested from the user via MCP elicitation.
//
// Optionally, pre-configured accounts can be provided via EMAIL_MCP_ACCOUNTS
// or a config file; those are served at /{domain} without query params.
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
	configPath := flag.String("config", envOr("EMAIL_MCP_CONFIG", ""), "optional path to accounts JSON file (EMAIL_MCP_ACCOUNTS env var takes precedence); without it the server runs in 0-config mode")
	flag.Parse()

	accs, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}
	byDomain := accs.ByDomain()

	if len(byDomain) == 0 {
		log.Printf("email-mcp: 0-config mode — any /{domain}?email=user@domain is accepted;")
		log.Printf("             server settings via DNS auto-discovery or query params, credentials via elicitation")
	} else {
		log.Printf("email-mcp: serving %d pre-configured account(s):", len(byDomain))
		for domain, acc := range byDomain {
			log.Printf("  /%s  ->  %s", domain, acc.Email)
		}
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
