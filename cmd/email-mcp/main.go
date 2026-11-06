// Command email-mcp serves mail accounts over MCP Streamable HTTP.
//
// The server is stateless: any domain path is accepted, e.g.
//
//	https://mcp.example.org/gmail.com?username=user
//
// IMAP/SMTP endpoints are auto-discovered via DNS SRV/MX records (or given
// explicitly as query params: imap_host, imap_port, imap_tls, smtp_host, ...),
// and the credential is carried by the client request (X-Email-Password or
// Authorization: Bearer header, else MCP elicitation).
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

	log.Printf("email-mcp: stateless — any /{domain}?username=user is accepted (user@domain);")
	log.Printf("             server settings via DNS auto-discovery or query params, credentials via header or elicitation")

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
