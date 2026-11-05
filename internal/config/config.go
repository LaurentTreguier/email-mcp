// Package config loads mail account configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IMAPConfig describes how to reach an IMAP server.
type IMAPConfig struct {
	Host string `json:"host"` // e.g. "imap.gmail.com"
	Port int    `json:"port"` // e.g. 993
	TLS  bool   `json:"tls"`  // implicit TLS; if false, STARTTLS is attempted
	// Insecure dials without any TLS. Only for local testing.
	Insecure bool `json:"insecure,omitempty"`
	// InsecureSkipVerify disables certificate verification. Only for local testing.
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`
}

// SMTPConfig describes how to reach an SMTP submission server.
type SMTPConfig struct {
	Host string `json:"host"` // e.g. "smtp.gmail.com"
	Port int    `json:"port"` // e.g. 587
	TLS  bool   `json:"tls"`  // implicit TLS (465); if false, STARTTLS is used
	// Insecure dials without any TLS. Only for local testing.
	Insecure bool `json:"insecure,omitempty"`
	// InsecureSkipVerify disables certificate verification. Only for local testing.
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`
}

// Account is a single mail account.
type Account struct {
	// Email is the account's mail address, e.g. "user@gmail.com". Its domain
	// selects the account on the HTTP endpoint: https://host/gmail.com
	Email string `json:"email"`
	// DisplayName is the identity used in the From header when sending.
	DisplayName string `json:"display_name,omitempty"`
	// Username for IMAP/SMTP login. Defaults to Email.
	Username string `json:"username,omitempty"`
	// PasswordEnv names the environment variable holding the password or
	// app-password. If PasswordEnv is empty, Password is used directly.
	PasswordEnv string `json:"password_env,omitempty"`
	Password    string `json:"password,omitempty"`
	// AuthMechanism selects the SASL mechanism: "plain" (default), "login",
	// or "xoauth2" (PasswordEnv then holds the OAuth2 access token).
	AuthMechanism string     `json:"auth_mechanism,omitempty"`
	IMAP          IMAPConfig `json:"imap"`
	SMTP          SMTPConfig `json:"smtp"`
	// SentMailbox is where copies of sent messages are stored (IMAP APPEND).
	// Defaults to "Sent".
	SentMailbox string `json:"sent_mailbox,omitempty"`
	// DraftsMailbox is where drafts are stored. Defaults to "Drafts".
	DraftsMailbox string `json:"drafts_mailbox,omitempty"`
}

// Domain returns the domain part of the account email, used as the URL path.
func (a *Account) Domain() string {
	_, domain, _ := strings.Cut(a.Email, "@")
	return strings.ToLower(domain)
}

// ResolvePassword reads the password from the configured env var, if any.
func (a *Account) ResolvePassword() (string, error) {
	if a.PasswordEnv != "" {
		v := os.Getenv(a.PasswordEnv)
		if v == "" {
			return "", fmt.Errorf("environment variable %s is not set", a.PasswordEnv)
		}
		return v, nil
	}
	return a.Password, nil
}

// Accounts is the root configuration: a list of accounts.
type Accounts struct {
	Accounts []Account `json:"accounts"`
}

// EnvVarName is the environment variable consulted for account configuration.
// It may contain inline JSON (starting with '{') or a path to a JSON file.
const EnvVarName = "EMAIL_MCP_ACCOUNTS"

// Load reads accounts from the EMAIL_MCP_ACCOUNTS env var if set, otherwise
// from the config file at path (default ~/.config/email-mcp/accounts.json).
func Load(path string) (*Accounts, error) {
	if v := os.Getenv(EnvVarName); v != "" {
		if strings.HasPrefix(strings.TrimSpace(v), "{") {
			return parse([]byte(v))
		}
		data, err := os.ReadFile(v)
		if err != nil {
			return nil, fmt.Errorf("reading accounts file %s: %w", v, err)
		}
		return parse(data)
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".config", "email-mcp", "accounts.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading accounts file %s (set %s to provide accounts): %w", path, EnvVarName, err)
	}
	return parse(data)
}

// Normalize fills in default values.
func (a *Account) Normalize() {
	if a.Username == "" {
		a.Username = a.Email
	}
	if a.SentMailbox == "" {
		a.SentMailbox = "Sent"
	}
	if a.DraftsMailbox == "" {
		a.DraftsMailbox = "Drafts"
	}
	if a.IMAP.Port == 0 {
		if a.IMAP.TLS {
			a.IMAP.Port = 993
		} else {
			a.IMAP.Port = 143
		}
	}
	if a.SMTP.Port == 0 {
		if a.SMTP.TLS {
			a.SMTP.Port = 465
		} else {
			a.SMTP.Port = 587
		}
	}
}

func parse(data []byte) (*Accounts, error) {
	var accs Accounts
	if err := json.Unmarshal(data, &accs); err != nil {
		return nil, fmt.Errorf("parsing accounts JSON: %w", err)
	}
	if len(accs.Accounts) == 0 {
		return nil, fmt.Errorf("no accounts configured")
	}
	seen := map[string]bool{}
	for i := range accs.Accounts {
		a := &accs.Accounts[i]
		if a.Email == "" {
			return nil, fmt.Errorf("account %d: email is required", i)
		}
		a.Normalize()
		d := a.Domain()
		if seen[d] {
			return nil, fmt.Errorf("duplicate account for domain %q", d)
		}
		seen[d] = true
	}
	return &accs, nil
}

// ByDomain indexes accounts by email domain.
func (a *Accounts) ByDomain() map[string]*Account {
	m := make(map[string]*Account, len(a.Accounts))
	for i := range a.Accounts {
		m[a.Accounts[i].Domain()] = &a.Accounts[i]
	}
	return m
}
