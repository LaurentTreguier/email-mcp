// Package mail manages IMAP sessions and high-level mailbox operations.
package mail

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-sasl"

	"email-mcp/internal/config"
)

// Session is a lazily-established, auto-reconnecting IMAP session for one account.
type Session struct {
	acc *config.Account

	mu     sync.Mutex
	client *imapclient.Client
}

// NewSession creates a session for the account. The connection is established lazily.
func NewSession(acc *config.Account) *Session {
	acc.Normalize()
	return &Session{acc: acc}
}

func (s *Session) tlsConfig(insecure bool) *tls.Config {
	return &tls.Config{ServerName: s.acc.IMAP.Host, InsecureSkipVerify: insecure} //nolint:gosec // opt-in for testing
}

func (s *Session) dial() (*imapclient.Client, error) {
	addr := net.JoinHostPort(s.acc.IMAP.Host, strconv.Itoa(s.acc.IMAP.Port))
	opts := &imapclient.Options{
		TLSConfig: s.tlsConfig(s.acc.IMAP.InsecureSkipVerify),
		WordDecoder: &mime.WordDecoder{
			CharsetReader: charset.Reader,
		},
	}
	var c *imapclient.Client
	var err error
	switch {
	case s.acc.IMAP.TLS:
		c, err = imapclient.DialTLS(addr, opts)
	case s.acc.IMAP.Insecure:
		c, err = imapclient.DialInsecure(addr, opts)
	default:
		c, err = imapclient.DialStartTLS(addr, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("dialing IMAP %s: %w", addr, err)
	}

	password := s.acc.Password
	switch s.acc.AuthMechanism {
	case "", "plain":
		if err := c.Login(s.acc.Username, password).Wait(); err != nil {
			c.Close()
			return nil, fmt.Errorf("IMAP login: %w", err)
		}
	case "login":
		if err := c.Authenticate(sasl.NewLoginClient(s.acc.Username, password)); err != nil {
			c.Close()
			return nil, fmt.Errorf("IMAP AUTHENTICATE LOGIN: %w", err)
		}
	case "xoauth2":
		if err := c.Authenticate(sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
			Username: s.acc.Username,
			Token:    password,
		})); err != nil {
			c.Close()
			return nil, fmt.Errorf("IMAP XOAUTH2: %w", err)
		}
	default:
		c.Close()
		return nil, fmt.Errorf("unsupported auth_mechanism %q", s.acc.AuthMechanism)
	}
	return c, nil
}

// withClient runs fn with a connected client, reconnecting once on failure.
func (s *Session) withClient(fn func(*imapclient.Client) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		c, err := s.dial()
		if err != nil {
			return err
		}
		s.client = c
	}
	err := fn(s.client)
	if err != nil && s.client.Closed() != nil {
		// Connection dropped: reconnect and retry once.
		s.client.Close()
		s.client = nil
		c, derr := s.dial()
		if derr != nil {
			return derr
		}
		s.client = c
		err = fn(s.client)
	}
	return err
}

// Close terminates the session.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Logout().Wait()
		s.client.Close()
		s.client = nil
	}
}

// Account returns the underlying account configuration.
func (s *Session) Account() *config.Account { return s.acc }

// keepaliveInterval is how often idle connections are pinged with NOOP.
const keepaliveInterval = 25 * time.Minute

// KeepAlive starts a background NOOP loop to keep the connection alive.
// The goroutine exits when stop is closed or the process ends.
func (s *Session) KeepAlive(stop <-chan struct{}) {
	t := time.NewTicker(keepaliveInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.withClient(func(c *imapclient.Client) error {
				return c.Noop().Wait()
			})
		}
	}
}
