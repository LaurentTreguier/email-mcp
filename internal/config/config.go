// Package config defines the mail account description. The server is
// stateless: every account is captured once, at OAuth consent time, and
// travels inside the encrypted claim of the issued access token — never in
// a server-side config file or env var.
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// IMAPConfig describes how to reach an IMAP server.
type IMAPConfig struct {
	Host string // e.g. "imap.gmail.com"
	Port int    // e.g. 993
	TLS  bool   // implicit TLS; if false, STARTTLS is attempted
	// Insecure dials without any TLS. Only for local testing.
	Insecure bool
	// InsecureSkipVerify disables certificate verification. Only for local testing.
	InsecureSkipVerify bool
}

// SMTPConfig describes how to reach an SMTP submission server.
type SMTPConfig struct {
	Host string // e.g. "smtp.gmail.com"
	Port int    // e.g. 587
	TLS  bool   // implicit TLS (465); if false, STARTTLS is used
	// Insecure dials without any TLS. Only for local testing.
	Insecure bool
	// InsecureSkipVerify disables certificate verification. Only for local testing.
	InsecureSkipVerify bool
}

// Account is a single mail account, captured at OAuth consent time and
// carried inside the issued access token.
type Account struct {
	// Email is the account's mail address, e.g. "user@gmail.com".
	Email string
	// DisplayName is the identity used in the From header when sending.
	DisplayName string
	// Username for IMAP/SMTP login. Defaults to Email.
	Username string
	// Password (or XOAUTH2 access token) captured at consent time and
	// carried inside the access token. Never part of the URL.
	Password string
	// AuthMechanism selects the SASL mechanism: "plain" (default), "login",
	// or "xoauth2" (Password then holds the OAuth2 access token).
	AuthMechanism string
	IMAP          IMAPConfig
	SMTP          SMTPConfig
	// SentMailbox is where copies of sent messages are stored (IMAP APPEND).
	// Defaults to "Sent".
	SentMailbox string
	// DraftsMailbox is where drafts are stored. Defaults to "Drafts".
	DraftsMailbox string
}

// AccountFromFormValues builds an account from the OAuth consent form
// fields (the single place account configuration is entered). Empty
// IMAP/SMTP settings are left for DNS auto-discovery.
func AccountFromFormValues(f url.Values) (*Account, error) {
	get := func(k string) string { return strings.TrimSpace(f.Get(k)) }
	getBool := func(k string) bool {
		_, ok := f[k]
		return ok
	}
	getInt := func(k string) (int, error) {
		s := get(k)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 || n > 65535 {
			return 0, fmt.Errorf("invalid %s: %q", k, s)
		}
		return n, nil
	}

	email := get("email")
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return nil, fmt.Errorf("missing or invalid email (full mail address required, e.g. user@gmail.com)")
	}
	if get("password") == "" {
		return nil, fmt.Errorf("password is required")
	}

	acc := &Account{
		Email:         email,
		Password:      get("password"),
		DisplayName:   get("display_name"),
		Username:      get("username"),
		AuthMechanism: get("auth_mechanism"),
		SentMailbox:   get("sent_mailbox"),
		DraftsMailbox: get("drafts_mailbox"),
	}
	if acc.Username == "" {
		acc.Username = email
	}
	if acc.AuthMechanism == "" {
		acc.AuthMechanism = "plain"
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
		switch {
		case getBool(prefix + "_insecure"):
			return "insecure"
		case getBool(prefix + "_tls"):
			return "tls"
		case getBool(prefix + "_starttls"):
			return "starttls"
		}
		return ""
	}
	acc.SetIMAP(get("imap_host"), imapPort, tlsMode("imap"))
	acc.SetSMTP(get("smtp_host"), smtpPort, tlsMode("smtp"))
	if getBool("imap_skip_verify") {
		acc.IMAP.InsecureSkipVerify = true
	}
	if getBool("smtp_skip_verify") {
		acc.SMTP.InsecureSkipVerify = true
	}

	acc.Normalize()
	return acc, nil
}

// Domain returns the domain part of the account email (drives DNS
// auto-discovery of the IMAP/SMTP servers).
func (a *Account) Domain() string {
	_, domain, _ := strings.Cut(a.Email, "@")
	return strings.ToLower(domain)
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
