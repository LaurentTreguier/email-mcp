// Command email-mcp serves mail accounts over MCP Streamable HTTP.
//
// The server is stateless with a single fixed endpoint:
//
//	https://mcp.example.org/mcp
//
// The whole account setup travels in the client's X-Email-* request headers
// (X-Email-Address required; X-Email-Password or an Authorization Bearer
// token for the credential; optional IMAP/SMTP overrides, else DNS
// auto-discovery).
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
	log.Printf("             account via X-Email-* headers, server settings via DNS auto-discovery or headers, credentials via header or elicitation")

	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpserver.New(),
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
