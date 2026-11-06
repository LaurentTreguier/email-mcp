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
// A missing config is NOT an error in 0-config mode: an empty Accounts is
// returned and accounts can be created per-request from query params.
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
			return &Accounts{}, nil
		}
		path = filepath.Join(home, ".config", "email-mcp", "accounts.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Accounts{}, nil // 0-config mode
		}
		return nil, fmt.Errorf("reading accounts file %s: %w", path, err)
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
	// Port/TLS defaults are only applied when a host is set (i.e. not left
	// for DNS auto-discovery); see SetIMAP/SetSMTP.
	if a.IMAP.Host != "" && a.IMAP.Port == 0 {
		if a.IMAP.TLS {
			a.IMAP.Port = 993
		} else {
			a.IMAP.Port = 143
		}
	}
	if a.SMTP.Host != "" && a.SMTP.Port == 0 {
		if a.SMTP.TLS {
			a.SMTP.Port = 465
		} else {
			a.SMTP.Port = 587
		}
	}
}

// SetIMAP sets the IMAP endpoint and derives the port/TLS mode when not given.
func (a *Account) SetIMAP(host string, port int, tlsMode string) {
	a.IMAP.Host = host
	switch tlsMode {
	case "tls":
		a.IMAP.TLS, a.IMAP.Insecure = true, false
		if port == 0 {
			port = 993
		}
	case "starttls":
		a.IMAP.TLS, a.IMAP.Insecure = false, false
		if port == 0 {
			port = 143
		}
	case "insecure":
		a.IMAP.TLS, a.IMAP.Insecure = false, true
		if port == 0 {
			port = 143
		}
	}
	a.IMAP.Port = port
}

// SetSMTP sets the SMTP endpoint and derives the port/TLS mode when not given.
func (a *Account) SetSMTP(host string, port int, tlsMode string) {
	a.SMTP.Host = host
	switch tlsMode {
	case "tls":
		a.SMTP.TLS, a.SMTP.Insecure = true, false
		if port == 0 {
			port = 465
		}
	case "starttls":
		a.SMTP.TLS, a.SMTP.Insecure = false, false
		if port == 0 {
			port = 587
		}
	case "insecure":
		a.SMTP.TLS, a.SMTP.Insecure = false, true
		if port == 0 {
			port = 25
		}
	}
	a.SMTP.Port = port
}

func parse(data []byte) (*Accounts, error) {
	var accs Accounts
	if err := json.Unmarshal(data, &accs); err != nil {
		return nil, fmt.Errorf("parsing accounts JSON: %w", err)
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

// FromQuery builds an account from URL query parameters. Supported params:
//
//	email              (required) user's mail address, e.g. user@gmail.com
//	display_name       display name for the From header
//	username           login username (default: email)
//	imap_host, imap_port, imap_tls / imap_starttls / imap_insecure
//	smtp_host, smtp_port, smtp_tls / smtp_starttls / smtp_insecure
//	auth_mechanism     plain (default) | login | xoauth2
//	sent_mailbox, drafts_mailbox
//
// The password is never taken from the query string; it is obtained from the
// user via elicitation or from the account config/env.
func FromQuery(q map[string][]string) (*Account, error) {
	get := func(key string) string {
		if v := q[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	getBool := func(keys ...string) (bool, bool) {
		for _, k := range keys {
			if v := get(k); v != "" {
				return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes"), true
			}
		}
		return false, false
	}
	getInt := func(key string) (int, error) {
		s := get(key)
		if s == "" {
			return 0, nil
		}
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 || n > 65535 {
			return 0, fmt.Errorf("invalid %s=%q", key, s)
		}
		return n, nil
	}

	email := get("email")
	if email == "" {
		return nil, fmt.Errorf("missing required query param 'email' (e.g. ?email=user@gmail.com)")
	}
	if _, dom, ok := strings.Cut(email, "@"); !ok || dom == "" {
		return nil, fmt.Errorf("invalid email %q", email)
	}

	acc := &Account{
		Email:         email,
		DisplayName:   get("display_name"),
		Username:      get("username"),
		AuthMechanism: get("auth_mechanism"),
		SentMailbox:   get("sent_mailbox"),
		DraftsMailbox: get("drafts_mailbox"),
	}
	imapPort, err := getInt("imap_port")
	if err != nil {
		return nil, err
	}
	smtpPort, err := getInt("smtp_port")
	if err != nil {
		return nil, err
	}
	tlsMode := func(prefix string) string {
		if v, ok := getBool(prefix + "_insecure"); ok && v {
			return "insecure"
		}
		if v, ok := getBool(prefix + "_tls"); ok {
			if v {
				return "tls"
			}
			return "starttls"
		}
		if v, ok := getBool(prefix + "_starttls"); ok && v {
			return "starttls"
		}
		return ""
	}
	acc.SetIMAP(get("imap_host"), imapPort, tlsMode("imap"))
	acc.SetSMTP(get("smtp_host"), smtpPort, tlsMode("smtp"))

	acc.Normalize()
	return acc, nil
}

// ByDomain indexes accounts by email domain.
func (a *Accounts) ByDomain() map[string]*Account {
	m := make(map[string]*Account, len(a.Accounts))
	for i := range a.Accounts {
		m[a.Accounts[i].Domain()] = &a.Accounts[i]
	}
	return m
}
