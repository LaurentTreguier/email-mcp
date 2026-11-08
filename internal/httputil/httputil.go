// Package httputil holds small request helpers shared by the HTTP and OAuth
// servers.
package httputil

import (
	"net/http"
	"strings"
)

// TLSEnabled reports whether clients reach the server over TLS: either the
// connection itself is TLS, or a reverse proxy forwarded the original scheme
// in X-Forwarded-Proto: https.
func TLSEnabled(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	p := r.Header.Get("X-Forwarded-Proto")
	if p == "" {
		return false
	}
	return strings.TrimSpace(strings.Split(p, ",")[0]) == "https"
}

// Scheme returns the scheme clients use to reach the server: "https" when
// TLSEnabled, else "http".
func Scheme(r *http.Request) string {
	if TLSEnabled(r) {
		return "https"
	}
	return "http"
}
