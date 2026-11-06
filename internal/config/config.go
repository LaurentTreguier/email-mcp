// Package config defines the per-request mail account description. The
// server is stateless: every account is fully described by the request
// (URL path domain + query params + credential header), never by a
// server-side config file or env var.
package config

import (
	"fmt"
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

// Account is a single mail account, built per request from the URL path,
// query params and credential headers.
type Account struct {
	// Email is the account's mail address, e.g. "user@gmail.com". Its domain
	// is the URL path: https://host/gmail.com
	Email string
	// DisplayName is the identity used in the From header when sending.
	DisplayName string
	// Username for IMAP/SMTP login. Defaults to Email.
	Username string
	// Password (or XOAUTH2 access token) supplied by the client in a
	// credential header, or via elicitation. Never part of the URL.
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

// Domain returns the domain part of the account email, used as the URL path.
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

// FromQuery builds an account for the URL path domain from query parameters.
// Supported params:
//
//	username           (required) login name: either the local part
//	                   (e.g. "user" for /gmail.com → user@gmail.com) or a
//	                   full address whose domain matches the path
//	display_name       display name for the From header
//	imap_host, imap_port, imap_tls / imap_starttls / imap_insecure
//	smtp_host, smtp_port, smtp_tls / smtp_starttls / smtp_insecure
//	auth_mechanism     plain (default) | login | xoauth2
//	sent_mailbox, drafts_mailbox
//
// The mail address is derived as username@domain; there is no separate email
//
//	param. The password is never taken from the query string; it comes from a
//	credential header or via elicitation.
func FromQuery(domain string, q map[string][]string) (*Account, error) {
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

	username := get("username")
	if username == "" {
		return nil, fmt.Errorf("missing required query param 'username' (local part or full address, e.g. /%s?username=user)", domain)
	}
	email := username
	login := username
	if local, dom, ok := strings.Cut(username, "@"); ok {
		if local == "" || dom == "" {
			return nil, fmt.Errorf("invalid username %q", username)
		}
		if !strings.EqualFold(dom, domain) {
			return nil, fmt.Errorf("username %q domain does not match path domain %q", username, domain)
		}
	} else {
		// Bare local part: the full address (and, by default, the login
		// name, as most providers require it) is local@domain.
		email = username + "@" + domain
		login = email
	}

	acc := &Account{
		Email:         email,
		DisplayName:   get("display_name"),
		Username:      login,
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
