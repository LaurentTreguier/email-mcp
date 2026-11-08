package httputil

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTLSEnabled(t *testing.T) {
	newReq := func(fwdProto string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://example.org/", nil)
		if fwdProto != "" {
			r.Header.Set("X-Forwarded-Proto", fwdProto)
		}
		return r
	}
	cases := []struct {
		name     string
		req      *http.Request
		expected bool
	}{
		{"plain HTTP", newReq(""), false},
		{"direct TLS", func() *http.Request { r := newReq(""); r.TLS = &tls.ConnectionState{}; return r }(), true},
		{"forwarded https", newReq("https"), true},
		{"forwarded https in list", newReq("https, http"), true},
		{"forwarded http", newReq("http"), false},
		{"direct TLS overrides forwarded http", func() *http.Request { r := newReq("http"); r.TLS = &tls.ConnectionState{}; return r }(), true},
	}
	for _, c := range cases {
		if got := TLSEnabled(c.req); got != c.expected {
			t.Errorf("%s: TLSEnabled = %v, want %v", c.name, got, c.expected)
		}
		wantScheme := "http"
		if c.expected {
			wantScheme = "https"
		}
		if got := Scheme(c.req); got != wantScheme {
			t.Errorf("%s: Scheme = %q, want %q", c.name, got, wantScheme)
		}
	}
}
