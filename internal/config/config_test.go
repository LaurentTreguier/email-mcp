package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromEnvInlineJSON(t *testing.T) {
	t.Setenv(EnvVarName, `{"accounts":[{"email":"user@gmail.com","password_env":"GMAIL_PASS","imap":{"host":"imap.gmail.com","tls":true},"smtp":{"host":"smtp.gmail.com"}}]}`)
	t.Setenv("GMAIL_PASS", "app-password")

	accs, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	byDomain := accs.ByDomain()
	acc, ok := byDomain["gmail.com"]
	if !ok {
		t.Fatal("gmail.com account not found")
	}
	if acc.IMAP.Port != 993 {
		t.Errorf("default IMAP port: got %d, want 993 (tls default)", acc.IMAP.Port)
	}
	if acc.SMTP.Port != 587 {
		t.Errorf("default SMTP port: got %d, want 587", acc.SMTP.Port)
	}
	if acc.SentMailbox != "Sent" || acc.DraftsMailbox != "Drafts" {
		t.Errorf("default mailboxes not applied: %+v", acc)
	}
	pw, err := acc.ResolvePassword()
	if err != nil || pw != "app-password" {
		t.Errorf("ResolvePassword: %q, %v", pw, err)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	content := `{"accounts":[{"email":"a@Example.ORG","password":"x","imap":{"host":"h","port":143},"smtp":{"host":"s","port":25}}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVarName, "")
	accs, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := accs.ByDomain()["example.org"]; !ok {
		t.Fatal("domain not lowercased/indexed")
	}
}

func TestDuplicateDomainRejected(t *testing.T) {
	t.Setenv(EnvVarName, `{"accounts":[{"email":"a@x.com","imap":{"host":"h"},"smtp":{"host":"s"}},{"email":"b@X.com","imap":{"host":"h"},"smtp":{"host":"s"}}]}`)
	if _, err := Load(""); err == nil {
		t.Fatal("expected duplicate domain error")
	}
}

func TestMissingPasswordEnv(t *testing.T) {
	acc := &Account{Email: "u@x.com", PasswordEnv: "DEFINITELY_NOT_SET_VAR"}
	if _, err := acc.ResolvePassword(); err == nil {
		t.Fatal("expected error for missing env var")
	}
}
