// Package discovery finds IMAP/SMTP server settings for an email domain
// automatically via DNS (RFC 6186 SRV records, RFC 7505 MX, well-known ports).
package discovery

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// ServerSettings holds discovered endpoints for a mail domain.
type ServerSettings struct {
	IMAPHost string
	IMAPPort int
	IMAPTLS  bool // implicit TLS (993)
	SMTPHost string
	SMTPPort int
	SMTPTLS  bool // implicit TLS (465); false means STARTTLS on 587
}

// Well-known ports used when no SRV record exists.
const (
	defaultIMAPSPort = 993 // imaps
	defaultSMTPSPort = 465 // smtps
)

// Discover looks up SRV records for imap/imaps/submission/submissions for the
// domain, falls back to the MX host (RFC 7505) for SMTP, then to the
// well-known hostnames imap.<domain> / smtp.<domain>.
func Discover(domain string) (*ServerSettings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := net.DefaultResolver

	s := &ServerSettings{}

	// SRV: _imaps._tcp, _imap._tcp, _submissions._tcp, _submission._tcp
	if host, port, ok := lookupSRVFunc(ctx, res, "imaps", domain); ok {
		s.IMAPHost, s.IMAPPort, s.IMAPTLS = host, port, true
	} else if host, port, ok := lookupSRVFunc(ctx, res, "imap", domain); ok {
		s.IMAPHost, s.IMAPPort, s.IMAPTLS = host, port, false
	}
	if host, port, ok := lookupSRVFunc(ctx, res, "submissions", domain); ok {
		s.SMTPHost, s.SMTPPort, s.SMTPTLS = host, port, true
	} else if host, port, ok := lookupSRVFunc(ctx, res, "submission", domain); ok {
		s.SMTPHost, s.SMTPPort, s.SMTPTLS = host, port, false
	}

	// MX fallback for SMTP if no submission SRV found.
	if s.SMTPHost == "" {
		if host, ok := lookupMXFunc(ctx, res, domain); ok {
			s.SMTPHost = host
		}
	}

	// Hostname fallbacks with well-known implicit-TLS ports.
	if s.IMAPHost == "" {
		s.IMAPHost = "imap." + domain
		s.IMAPPort, s.IMAPTLS = defaultIMAPSPort, true
	}
	if s.SMTPHost == "" {
		s.SMTPHost = "smtp." + domain
		s.SMTPPort, s.SMTPTLS = defaultSMTPSPort, true
	}

	if err := verifyReachable(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

// Account is the subset of config.Account needed to fill in discovered
// server settings. Shared by the OAuth consent flow and the /mcp request
// path so both resolve servers identically.
type Account interface {
	Domain() string
	IMAPHost() string
	SMTPHost() string
	SetIMAP(host string, port int, tlsMode string)
	SetSMTP(host string, port int, tlsMode string)
}

// FillDefaults auto-discovers any missing IMAP/SMTP settings of acc from its
// email domain and applies them in place.
func FillDefaults(acc Account) error {
	if acc.IMAPHost() != "" && acc.SMTPHost() != "" {
		return nil
	}
	discovered, err := Discover(acc.Domain())
	if err != nil {
		return fmt.Errorf("auto-discovery for %s failed: %w (set imap_host/smtp_host explicitly)", acc.Domain(), err)
	}
	if acc.IMAPHost() == "" {
		mode := "tls"
		if !discovered.IMAPTLS {
			mode = "starttls"
		}
		acc.SetIMAP(discovered.IMAPHost, discovered.IMAPPort, mode)
	}
	if acc.SMTPHost() == "" {
		mode := "tls"
		if !discovered.SMTPTLS {
			mode = "starttls"
		}
		acc.SetSMTP(discovered.SMTPHost, discovered.SMTPPort, mode)
	}
	return nil
}

// Function hooks (overridable in tests).
var (
	lookupSRVFunc = func(ctx context.Context, res *net.Resolver, service, domain string) (string, int, bool) {
		_, addrs, err := res.LookupSRV(ctx, service, "tcp", domain)
		if err != nil || len(addrs) == 0 {
			return "", 0, false
		}
		host := strings.TrimSuffix(addrs[0].Target, ".")
		if host == "" || host == "." {
			return "", 0, false
		}
		return host, int(addrs[0].Port), true
	}
	lookupMXFunc = func(ctx context.Context, res *net.Resolver, domain string) (string, bool) {
		mxs, err := res.LookupMX(ctx, domain)
		if err != nil || len(mxs) == 0 {
			return "", false
		}
		sort.Slice(mxs, func(i, j int) bool { return mxs[i].Pref < mxs[j].Pref })
		return strings.TrimSuffix(mxs[0].Host, "."), true
	}
	dialableFunc = func(ctx context.Context, host string, port int) error {
		d := net.Dialer{Timeout: 5 * time.Second}
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
		if err != nil {
			return err
		}
		c.Close()
		return nil
	}
)

// verifyReachable does a quick TCP dial to the discovered endpoints so we
// fail fast with a useful error instead of at first IMAP login.
func verifyReachable(ctx context.Context, s *ServerSettings) error {
	var errs []string
	if err := dialableFunc(ctx, s.IMAPHost, s.IMAPPort); err != nil {
		errs = append(errs, fmt.Sprintf("imap %s:%d: %v", s.IMAPHost, s.IMAPPort, err))
	}
	if err := dialableFunc(ctx, s.SMTPHost, s.SMTPPort); err != nil {
		errs = append(errs, fmt.Sprintf("smtp %s:%d: %v", s.SMTPHost, s.SMTPPort, err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("discovered servers not reachable: %s", strings.Join(errs, "; "))
	}
	return nil
}
