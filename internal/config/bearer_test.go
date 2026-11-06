package config

import (
	"strings"
	"testing"
)

func TestBearerRoundTrip(t *testing.T) {
	in := &Account{
		Email:         "user@example.org",
		Password:      "secret",
		Username:      "login-name",
		DisplayName:   "User Name",
		AuthMechanism: "login",
		SentMailbox:   "Sent Messages",
		DraftsMailbox: "DraftsX",
		IMAP:          IMAPConfig{Host: "imap.example.org", Port: 993, TLS: true},
		SMTP:          SMTPConfig{Host: "smtp.example.org", Port: 587},
	}
	enc := in.EncodeBearer()
	if strings.Contains(enc, "=") {
		t.Errorf("bearer should be raw-url base64 (no padding): %q", enc)
	}
	out := DecodeBearerAccount(enc)
	if out == nil {
		t.Fatal("DecodeBearerAccount returned nil")
	}
	if out.Email != in.Email || out.Password != in.Password || out.Username != in.Username ||
		out.DisplayName != in.DisplayName || out.AuthMechanism != in.AuthMechanism ||
		out.SentMailbox != in.SentMailbox || out.DraftsMailbox != in.DraftsMailbox {
		t.Errorf("round trip mismatch: %+v", out)
	}
	if out.IMAP.Host != "imap.example.org" || out.IMAP.Port != 993 || !out.IMAP.TLS {
		t.Errorf("imap mismatch: %+v", out.IMAP)
	}
	if out.SMTP.Host != "smtp.example.org" || out.SMTP.Port != 587 || out.SMTP.TLS || out.SMTP.Insecure {
		t.Errorf("smtp mismatch: %+v", out.SMTP)
	}
}

func TestDecodeBearerAccountRejectsNonAccounts(t *testing.T) {
	for _, tok := range []string{
		"not-base64!!!",
		"aGVsbG8",                 // "hello" — valid base64, not JSON
		"eyJmb28iOiJiYXIifQ",      // {"foo":"bar"} — JSON but no email
		"an opaque xoauth2 token", // raw token
		"",
	} {
		if acc := DecodeBearerAccount(tok); acc != nil {
			t.Errorf("DecodeBearerAccount(%q) = %+v, want nil", tok, acc)
		}
	}
}

func TestFromHeadersPrefersPackedBearer(t *testing.T) {
	acc := &Account{Email: "packed@example.org", Password: "pw"}
	h := hdr(
		HeaderAddress, "header@example.org",
		HeaderPassword, "header-pw",
		"Authorization", "Bearer "+acc.EncodeBearer(),
	)
	got, err := FromHeaders(h)
	if err != nil {
		t.Fatalf("FromHeaders: %v", err)
	}
	if got.Email != "packed@example.org" || got.Password != "pw" {
		t.Errorf("packed bearer should win over X-Email-* headers: %+v", got)
	}
}
