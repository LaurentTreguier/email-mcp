// Package config defines the per-request mail account description. The
// server is stateless: every account is fully described by the client's
// X-Email-* request headers, never by a server-side config file or env var.
package config

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Request headers that carry the account description. Clients that support
// named connections with custom auth (e.g. Mistral Vibe, VS Code mcp.json
// "headers") put the whole account setup here; the MCP endpoint URL itself
// stays fixed.
const (
	HeaderAddress       = "X-Email-Address"        // required: full mail address, e.g. user@gmail.com
	HeaderPassword      = "X-Email-Password"       // password / app password (or see Authorization Bearer)
	HeaderUsername      = "X-Email-Username"       // login name override (default: the address)
	HeaderDisplayName   = "X-Email-Display-Name"   // From-header display name
	HeaderAuthMech      = "X-Email-Auth-Mechanism" // plain (default) | login | xoauth2
	HeaderSentMailbox   = "X-Email-Sent-Mailbox"   // Sent folder name (default "Sent")
	HeaderDraftsMailbox = "X-Email-Drafts-Mailbox" // Drafts folder name (default "Drafts")

	HeaderIMAPHost       = "X-Email-IMAP-Host"
	HeaderIMAPPort       = "X-Email-IMAP-Port"
	HeaderIMAPTLS        = "X-Email-IMAP-TLS"         // implicit TLS (993)
	HeaderIMAPStartTLS   = "X-Email-IMAP-STARTTLS"    // STARTTLS (143)
	HeaderIMAPInsecure   = "X-Email-IMAP-Insecure"    // no TLS, local testing only
	HeaderIMAPSkipVerify = "X-Email-IMAP-Skip-Verify" // skip cert verification, local testing only
	HeaderSMTPHost       = "X-Email-SMTP-Host"
	HeaderSMTPPort       = "X-Email-SMTP-Port"
	HeaderSMTPTLS        = "X-Email-SMTP-TLS"         // implicit TLS (465)
	HeaderSMTPStartTLS   = "X-Email-SMTP-STARTTLS"    // STARTTLS (587)
	HeaderSMTPInsecure   = "X-Email-SMTP-Insecure"    // no TLS, local testing only
	HeaderSMTPSkipVerify = "X-Email-SMTP-Skip-Verify" // skip cert verification, local testing only
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

// Account is a single mail account, built per request from the client's
// X-Email-* headers.
type Account struct {
	// Email is the account's mail address, e.g. "user@gmail.com".
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

// FromHeaders builds an account from the client's X-Email-* request headers.
// The MCP endpoint URL is fixed; the whole account setup travels in the
// headers of each request (this is how MCP clients with named connections,
// e.g. Mistral Vibe or VS Code mcp.json "headers", carry per-connection
// auth).
//
// Required: X-Email-Address (full mail address, e.g. user@gmail.com).
// Credential: X-Email-Password, or Authorization: ****** (token used as the
// mail credential, e.g. XOAUTH2) — the password is never in the URL.
// Optional overrides (empty header = unset, DNS auto-discovery applies):
// X-Email-Username, X-Email-Display-Name, X-Email-Auth-Mechanism,
// X-Email-Sent-Mailbox, X-Email-Drafts-Mailbox, and the IMAP/SMTP set:
// X-Email-IMAP-Host/-Port/-TLS/-STARTTLS/-Insecure/-Skip-Verify and the
// X-Email-SMTP-* twins.
func FromHeaders(h http.Header) (*Account, error) {
	get := func(key string) string { return strings.TrimSpace(h.Get(key)) }
	getBool := func(key string) bool {
		v := get(key)
		return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
	getInt := func(key string) (int, error) {
		s := get(key)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 || n > 65535 {
			return 0, fmt.Errorf("invalid %s: %q", key, s)
		}
		return n, nil
	}

	email := get(HeaderAddress)
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return nil, fmt.Errorf("missing or invalid %s header (full mail address required, e.g. user@gmail.com)", HeaderAddress)
	}

	acc := &Account{
		Email:         email,
		DisplayName:   get(HeaderDisplayName),
		Username:      get(HeaderUsername),
		AuthMechanism: get(HeaderAuthMech),
		SentMailbox:   get(HeaderSentMailbox),
		DraftsMailbox: get(HeaderDraftsMailbox),
	}
	if acc.Username == "" {
		acc.Username = email
	}

	imapPort, err := getInt(HeaderIMAPPort)
	if err != nil {
		return nil, err
	}
	smtpPort, err := getInt(HeaderSMTPPort)
	if err != nil {
		return nil, err
	}
	tlsMode := func(prefix string) string {
		switch {
		case getBool(prefix + "-Insecure"):
			return "insecure"
		case getBool(prefix + "-TLS"):
			return "tls"
		case getBool(prefix + "-STARTTLS"):
			return "starttls"
		}
		return ""
	}
	acc.SetIMAP(get(HeaderIMAPHost), imapPort, tlsMode("X-Email-IMAP"))
	acc.SetSMTP(get(HeaderSMTPHost), smtpPort, tlsMode("X-Email-SMTP"))
	if getBool(HeaderIMAPSkipVerify) {
		acc.IMAP.InsecureSkipVerify = true
	}
	if getBool(HeaderSMTPSkipVerify) {
		acc.SMTP.InsecureSkipVerify = true
	}

	// The credential: custom password header, else a Bearer token (used as
	// the mail credential, e.g. an XOAUTH2 access token).
	if pw := get(HeaderPassword); pw != "" {
		acc.Password = pw
		if acc.AuthMechanism == "" {
			acc.AuthMechanism = "plain"
		}
	} else if a := h.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		acc.Password = strings.TrimPrefix(a, "Bearer ")
		if acc.AuthMechanism == "" {
			acc.AuthMechanism = "xoauth2"
		}
	}

	acc.Normalize()
	return acc, nil
}
