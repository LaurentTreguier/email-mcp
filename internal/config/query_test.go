package config

import (
	"net/url"
	"testing"
)

func TestFromQueryMinimal(t *testing.T) {
	q := url.Values{"username": {"user"}}
	acc, err := FromQuery("Gmail.com", q)
	if err != nil {
		t.Fatalf("FromQuery: %v", err)
	}
	if acc.Email != "user@Gmail.com" {
		t.Errorf("email = %q", acc.Email)
	}
	if acc.Domain() != "gmail.com" {
		t.Errorf("domain = %q", acc.Domain())
	}
	if acc.Username != "user@Gmail.com" {
		t.Errorf("username default = %q (full address)", acc.Username)
	}
	// No host given: left empty for auto-discovery, so no port/TLS default
	// is applied yet.
	if acc.IMAP.Host != "" || acc.SMTP.Host != "" {
		t.Errorf("expected empty hosts for discovery, got imap=%q smtp=%q", acc.IMAP.Host, acc.SMTP.Host)
	}
	if acc.IMAP.Port != 0 || acc.SMTP.Port != 0 {
		t.Errorf("ports should be unset until discovery, got imap=%d smtp=%d", acc.IMAP.Port, acc.SMTP.Port)
	}
}

func TestFromQueryFullAddressUsername(t *testing.T) {
	q := url.Values{"username": {"user@Example.ORG"}}
	acc, err := FromQuery("example.org", q)
	if err != nil {
		t.Fatalf("FromQuery: %v", err)
	}
	if acc.Email != "user@Example.ORG" || acc.Username != "user@Example.ORG" {
		t.Errorf("acc = %+v", acc)
	}
}

func TestFromQueryFull(t *testing.T) {
	q := url.Values{
		"username":       {"user-login"},
		"display_name":   {"User"},
		"imap_host":      {"mail.example.org"},
		"imap_port":      {"143"},
		"imap_starttls":  {"1"},
		"smtp_host":      {"smtp.example.org"},
		"smtp_port":      {"587"},
		"smtp_tls":       {"0"},
		"auth_mechanism": {"login"},
		"sent_mailbox":   {"Sent Messages"},
	}
	acc, err := FromQuery("example.org", q)
	if err != nil {
		t.Fatalf("FromQuery: %v", err)
	}
	if acc.Email != "user-login@example.org" {
		t.Errorf("email = %q", acc.Email)
	}
	if acc.IMAP.Host != "mail.example.org" || acc.IMAP.Port != 143 || acc.IMAP.TLS {
		t.Errorf("imap = %+v", acc.IMAP)
	}
	if acc.SMTP.Host != "smtp.example.org" || acc.SMTP.Port != 587 || acc.SMTP.TLS {
		t.Errorf("smtp = %+v (smtp_tls=0 should mean STARTTLS, not implicit TLS)", acc.SMTP)
	}
	if acc.Username != "user-login@example.org" || acc.AuthMechanism != "login" || acc.SentMailbox != "Sent Messages" {
		t.Errorf("account = %+v (bare username: login defaults to full address)", acc)
	}
	if acc.Password != "" {
		t.Errorf("password must never come from query params, got %q", acc.Password)
	}
}

func TestFromQueryMissingUsername(t *testing.T) {
	if _, err := FromQuery("example.org", url.Values{}); err == nil {
		t.Fatal("expected error when username is missing")
	}
}

func TestFromQueryBadUsername(t *testing.T) {
	if _, err := FromQuery("example.org", url.Values{"username": {"not-an-email@other.net"}}); err == nil {
		t.Fatal("expected error when full-address username domain mismatches path")
	}
	if _, err := FromQuery("example.org", url.Values{"username": {"@"}}); err == nil {
		t.Fatal("expected error for invalid username")
	}
}

func TestFromQueryBadPort(t *testing.T) {
	q := url.Values{"username": {"u"}, "imap_port": {"abc"}}
	if _, err := FromQuery("x.com", q); err == nil {
		t.Fatal("expected error for non-numeric port")
	}
}

func TestLoadMissingConfigIsZeroConfig(t *testing.T) {
	t.Setenv(EnvVarName, "")
	accs, err := Load(t.TempDir() + "/nonexistent.json")
	if err != nil {
		t.Fatalf("missing config should not be an error in 0-config mode: %v", err)
	}
	if len(accs.Accounts) != 0 {
		t.Fatalf("expected empty accounts, got %d", len(accs.Accounts))
	}
}
