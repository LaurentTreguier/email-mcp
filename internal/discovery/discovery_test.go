package discovery

import (
	"context"
	"errors"
	"net"
	"testing"
)

var errFake = errors.New("fake dial error")

// These tests use a custom resolver via the package-level hook so no real DNS
// is needed.
func TestDiscoverWithFakeResolver(t *testing.T) {
	old := lookupSRVFunc
	oldMX := lookupMXFunc
	oldDial := dialableFunc
	defer func() { lookupSRVFunc, lookupMXFunc, dialableFunc = old, oldMX, oldDial }()

	lookupSRVFunc = func(_ context.Context, _ *net.Resolver, service, domain string) (string, int, bool) {
		switch service {
		case "imaps":
			return "imap.mail.example.com", 993, true
		case "submission":
			return "smtp.example.com", 587, true
		}
		return "", 0, false
	}
	lookupMXFunc = func(_ context.Context, _ *net.Resolver, domain string) (string, bool) { return "", false }
	dialableFunc = func(_ context.Context, host string, port int) error { return nil }

	s, err := Discover("example.com")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if s.IMAPHost != "imap.mail.example.com" || s.IMAPPort != 993 || !s.IMAPTLS {
		t.Errorf("imap = %+v", s)
	}
	if s.SMTPHost != "smtp.example.com" || s.SMTPPort != 587 || s.SMTPTLS {
		t.Errorf("smtp = %+v (submission SRV should mean STARTTLS)", s)
	}
}

func TestDiscoverFallbacks(t *testing.T) {
	old := lookupSRVFunc
	oldMX := lookupMXFunc
	oldDial := dialableFunc
	defer func() { lookupSRVFunc, lookupMXFunc, dialableFunc = old, oldMX, oldDial }()

	lookupSRVFunc = func(_ context.Context, _ *net.Resolver, service, domain string) (string, int, bool) {
		return "", 0, false
	}
	lookupMXFunc = func(_ context.Context, _ *net.Resolver, domain string) (string, bool) { return "mx1.example.org", true }
	dialableFunc = func(_ context.Context, host string, port int) error { return nil }

	s, err := Discover("example.org")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if s.IMAPHost != "imap.example.org" || s.IMAPPort != 993 {
		t.Errorf("imap fallback = %+v", s)
	}
	if s.SMTPHost != "mx1.example.org" {
		t.Errorf("smtp should fall back to MX host, got %q", s.SMTPHost)
	}
}

func TestDiscoverUnreachable(t *testing.T) {
	old := lookupSRVFunc
	oldMX := lookupMXFunc
	oldDial := dialableFunc
	defer func() { lookupSRVFunc, lookupMXFunc, dialableFunc = old, oldMX, oldDial }()

	lookupSRVFunc = func(_ context.Context, _ *net.Resolver, service, domain string) (string, int, bool) {
		return "", 0, false
	}
	lookupMXFunc = func(_ context.Context, _ *net.Resolver, domain string) (string, bool) { return "", false }
	dialableFunc = func(_ context.Context, host string, port int) error { return errFake }

	if _, err := Discover("nowhere.invalid"); err == nil {
		t.Fatal("expected error when discovered servers are unreachable")
	}
}
