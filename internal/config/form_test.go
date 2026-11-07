package config

import (
	"net/url"
	"testing"
)

func vals(pairs ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		v.Set(pairs[i], pairs[i+1])
	}
	return v
}

func TestAccountFromFormValuesMinimal(t *testing.T) {
	acc, err := AccountFromFormValues(vals("email", "User@Gmail.com", "password", "secret"))
	if err != nil {
		t.Fatalf("AccountFromFormValues: %v", err)
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
	if acc.AuthMechanism != "plain" {
		t.Errorf("auth mechanism default = %q, want plain", acc.AuthMechanism)
	}
	if acc.Password != "secret" {
		t.Errorf("password = %q", acc.Password)
	}
	// No server settings given: left empty for DNS auto-discovery.
	if acc.IMAP.Host != "" || acc.SMTP.Host != "" {
		t.Errorf("expected empty hosts for discovery, got imap=%q smtp=%q", acc.IMAP.Host, acc.SMTP.Host)
	}
	if acc.SentMailbox != "Sent" || acc.DraftsMailbox != "Drafts" {
		t.Errorf("mailbox defaults = %q/%q", acc.SentMailbox, acc.DraftsMailbox)
	}
}

func TestAccountFromFormValuesFull(t *testing.T) {
	v := vals(
		"email", "user@example.org",
		"password", "pw",
		"username", "login-name",
		"display_name", "User Name",
		"auth_mechanism", "login",
		"imap_host", "imap.example.org",
		"imap_port", "1993",
		"imap_tls", "1",
		"imap_skip_verify", "1",
		"smtp_host", "smtp.example.org",
		"smtp_starttls", "1",
		"smtp_skip_verify", "1",
		"sent_mailbox", "Sent Items",
		"drafts_mailbox", "DraftsX",
	)
	acc, err := AccountFromFormValues(v)
	if err != nil {
		t.Fatalf("AccountFromFormValues: %v", err)
	}
	if acc.Username != "login-name" || acc.DisplayName != "User Name" || acc.AuthMechanism != "login" {
		t.Errorf("overrides not applied: %+v", acc)
	}
	if acc.IMAP.Host != "imap.example.org" || acc.IMAP.Port != 1993 || !acc.IMAP.TLS || !acc.IMAP.InsecureSkipVerify {
		t.Errorf("imap = %+v", acc.IMAP)
	}
	if acc.SMTP.Host != "smtp.example.org" || acc.SMTP.Port != 587 || acc.SMTP.TLS || !acc.SMTP.InsecureSkipVerify {
		t.Errorf("smtp = %+v", acc.SMTP)
	}
	if acc.SentMailbox != "Sent Items" || acc.DraftsMailbox != "DraftsX" {
		t.Errorf("mailboxes = %q/%q", acc.SentMailbox, acc.DraftsMailbox)
	}
}

func TestAccountFromFormValuesInsecure(t *testing.T) {
	v := vals("email", "u@e.com", "password", "p", "imap_host", "127.0.0.1", "imap_port", "143", "imap_insecure", "1", "smtp_host", "127.0.0.1", "smtp_insecure", "1")
	acc, err := AccountFromFormValues(v)
	if err != nil {
		t.Fatalf("AccountFromFormValues: %v", err)
	}
	if !acc.IMAP.Insecure || acc.IMAP.TLS {
		t.Errorf("imap = %+v, want insecure", acc.IMAP)
	}
	if !acc.SMTP.Insecure {
		t.Errorf("smtp = %+v, want insecure", acc.SMTP)
	}
}

func TestAccountFromFormValuesErrors(t *testing.T) {
	for _, v := range []url.Values{
		vals("password", "x"),                            // no email
		vals("email", "not-an-address", "password", "x"), // bad email
		vals("email", "u@e.com"),                         // no password
		vals("email", "u@e.com", "password", "x", "imap_port", "abc"),
		vals("email", "u@e.com", "password", "x", "smtp_port", "99999"),
	} {
		if _, err := AccountFromFormValues(v); err == nil {
			t.Errorf("expected error for %v", v)
		}
	}
}
