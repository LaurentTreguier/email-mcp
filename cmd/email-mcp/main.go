// Command email-mcp serves mail accounts over MCP Streamable HTTP.
//
// The server is stateless with a single fixed endpoint:
//
//	https://mcp.example.org/mcp
//
// Authentication is OAuth 2.0 only: the client sends an access token issued
// by the built-in authorization server as Authorization: ******; the whole
// account setup (captured at consent time) travels encrypted inside the
// token. Unset IMAP/SMTP servers are auto-discovered via DNS.
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

	"email-mcp/internal/httpserver"
)

func main() {
	addr := flag.String("addr", envOr("EMAIL_MCP_ADDR", ":8080"), "listen address for the Streamable HTTP server")
	flag.Parse()

	log.Printf("email-mcp: stateless — single MCP endpoint at /mcp;")
	log.Printf("             auth via OAuth 2.0 bearer token (account embedded, encrypted),")
	log.Printf("             server settings via DNS auto-discovery or the consent form")
	if v := os.Getenv("EMAIL_MCP_BASE_URL"); v != "" {
		log.Printf("             OAuth authorization server issuer: %s", v)
	} else {
		log.Printf("             OAuth authorization server issuer derived per request (set EMAIL_MCP_BASE_URL to fix it)")
	}
	switch {
	case os.Getenv("EMAIL_MCP_SIGNING_KEY_FILE") != "":
		log.Printf("             OAuth signing key: EMAIL_MCP_SIGNING_KEY_FILE=%s", os.Getenv("EMAIL_MCP_SIGNING_KEY_FILE"))
	case os.Getenv("EMAIL_MCP_SIGNING_KEY") != "":
		log.Printf("             OAuth signing key: EMAIL_MCP_SIGNING_KEY (inline PEM)")
	}
	if os.Getenv("EMAIL_MCP_TOKEN_SECRET") != "" {
		log.Printf("             OAuth token secret: EMAIL_MCP_TOKEN_SECRET (stable across restarts)")
	}

	handler, err := httpserver.New()
	if err != nil {
		log.Fatalf("startup: %v — set EMAIL_MCP_SIGNING_KEY or EMAIL_MCP_SIGNING_KEY_FILE (PEM RSA private key) and EMAIL_MCP_TOKEN_SECRET (32 bytes) so issued tokens survive restarts", err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
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
