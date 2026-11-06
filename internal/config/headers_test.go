package config

import (
	"net/http"
	"testing"
)

func hdr(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

func TestFromHeadersMinimal(t *testing.T) {
	acc, err := FromHeaders(hdr(HeaderAddress, "User@Gmail.com"))
	if err != nil {
		t.Fatalf("FromHeaders: %v", err)
	}
	if acc.Email != "User@Gmail.com" {
		t.Errorf("email = %q", acc.Email)
	}
	if acc.Domain() != "gmail.com" {
		t.Errorf("domain = %q", acc.Domain())
	}
	if acc.Username != "User@Gmail.com" {
		t.Errorf("username default = %q (full address)", acc.Username)
	}
	// No host headers given: left empty for auto-discovery, so no port/TLS
	// default is applied yet.
	if acc.IMAP.Host != "" || acc.SMTP.Host != "" {
		t.Errorf("expected empty hosts for discovery, got imap=%q smtp=%q", acc.IMAP.Host, acc.SMTP.Host)
	}
	if acc.IMAP.Port != 0 || acc.SMTP.Port != 0 {
		t.Errorf("ports should be unset until discovery, got imap=%d smtp=%d", acc.IMAP.Port, acc.SMTP.Port)
	}
	if acc.Password != "" {
		t.Errorf("no credential header → no password, got %q", acc.Password)
	}
}

func TestFromHeadersFull(t *testing.T) {
	acc, err := FromHeaders(hdr(
		HeaderAddress, "user@example.org",
		HeaderUsername, "user-login",
		HeaderDisplayName, "User",
		HeaderAuthMech, "login",
		HeaderIMAPHost, "mail.example.org",
		HeaderIMAPPort, "143",
		HeaderIMAPStartTLS, "1",
		HeaderSMTPHost, "smtp.example.org",
		HeaderSMTPPort, "587",
		HeaderSentMailbox, "Sent Messages",
		HeaderPassword, "secret",
		HeaderIMAPSkipVerify, "true",
	))
	if err != nil {
		t.Fatalf("FromHeaders: %v", err)
	}
	if acc.IMAP.Host != "mail.example.org" || acc.IMAP.Port != 143 || acc.IMAP.TLS {
		t.Errorf("imap = %+v", acc.IMAP)
	}
	if acc.SMTP.Host != "smtp.example.org" || acc.SMTP.Port != 587 || acc.SMTP.TLS {
		t.Errorf("smtp = %+v (no TLS flag → STARTTLS mode)", acc.SMTP)
	}
	if !acc.IMAP.InsecureSkipVerify {
		t.Errorf("imap skip-verify not set: %+v", acc.IMAP)
	}
	if acc.Username != "user-login" || acc.AuthMechanism != "login" || acc.SentMailbox != "Sent Messages" || acc.DisplayName != "User" {
		t.Errorf("account = %+v", acc)
	}
	if acc.Password != "secret" {
		t.Errorf("password header must set the credential: %+v", acc)
	}
}

func TestFromHeadersBearerIsCredential(t *testing.T) {
	acc, err := FromHeaders(hdr(
		HeaderAddress, "user@example.org",
		"Authorization", "Bearer tok123",
	))
	if err != nil {
		t.Fatalf("FromHeaders: %v", err)
	}
	if acc.Password != "tok123" {
		t.Errorf("bearer token should be used as the mail credential, got %q", acc.Password)
	}
	if acc.AuthMechanism != "xoauth2" {
		t.Errorf("bearer default mechanism should be xoauth2, got %q", acc.AuthMechanism)
	}
}

func TestFromHeadersPasswordHeaderWins(t *testing.T) {
	acc, err := FromHeaders(hdr(
		HeaderAddress, "user@example.org",
		HeaderPassword, "pw",
		"Authorization", "Bearer tok123",
	))
	if err != nil {
		t.Fatalf("FromHeaders: %v", err)
	}
	if acc.Password != "pw" || acc.AuthMechanism != "plain" {
		t.Errorf("X-Email-Password should win over Bearer: %+v", acc)
	}
}

func TestFromHeadersMissingAddress(t *testing.T) {
	if _, err := FromHeaders(http.Header{}); err == nil {
		t.Fatal("expected error when X-Email-Address is missing")
	}
}

func TestFromHeadersBadAddress(t *testing.T) {
	for _, bad := range []string{"no-at-sign", "@dom.com", "user@", ""} {
		if _, err := FromHeaders(hdr(HeaderAddress, bad)); err == nil {
			t.Errorf("expected error for address %q", bad)
		}
	}
}

func TestFromHeadersBadPort(t *testing.T) {
	if _, err := FromHeaders(hdr(HeaderAddress, "u@x.com", HeaderIMAPPort, "abc")); err == nil {
		t.Fatal("expected error for non-numeric port")
	}
}

func TestFromHeadersTLSModes(t *testing.T) {
	cases := []struct {
		header string
		value  string
		want   string // "tls" | "starttls" | "insecure"
	}{
		{HeaderIMAPTLS, "1", "tls"},
		{HeaderIMAPStartTLS, "true", "starttls"},
		{HeaderIMAPInsecure, "yes", "insecure"},
	}
	for _, c := range cases {
		acc, err := FromHeaders(hdr(HeaderAddress, "u@x.com", c.header, c.value))
		if err != nil {
			t.Fatalf("FromHeaders(%s=%s): %v", c.header, c.value, err)
		}
		switch c.want {
		case "tls":
			if !acc.IMAP.TLS || acc.IMAP.Insecure {
				t.Errorf("%s=%s → imap = %+v", c.header, c.value, acc.IMAP)
			}
		case "insecure":
			if !acc.IMAP.Insecure || acc.IMAP.TLS {
				t.Errorf("%s=%s → imap = %+v", c.header, c.value, acc.IMAP)
			}
		default:
			if acc.IMAP.TLS || acc.IMAP.Insecure {
				t.Errorf("%s=%s → imap = %+v", c.header, c.value, acc.IMAP)
			}
		}
	}
}
