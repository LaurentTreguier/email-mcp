package oauthserver

import (
	"fmt"
	"time"

	"email-mcp/internal/config"
	"email-mcp/internal/discovery"
	"email-mcp/internal/mail"
)

// verifyTimeout bounds the whole consent-time account verification so a
// blackholed server cannot hang the consent form.
const verifyTimeout = 20 * time.Second

// VerifyAccount validates an account at consent time. It is a package-level
// hook so tests (in this package and dependents) can stub the live IMAP/SMTP
// checks.
var VerifyAccount = verifyAccountLive

// verifyAccountLive validates an account with read-only, side-effect-free
// operations: DNS auto-discovery for unset servers (which already does a
// TCP reachability check), then IMAP dial+LOGIN+LIST+STATUS INBOX and SMTP
// dial+EHLO+AUTH+NOOP+QUIT. Nothing is sent, no flags change, no mailbox is
// selected read-write.
func verifyAccountLive(acc *config.Account) error {
	done := make(chan error, 1)
	go func() {
		if err := discovery.FillDefaults(acc); err != nil {
			done <- err
			return
		}
		sess := mail.NewSession(acc)
		defer sess.Close()
		if err := sess.VerifyIMAP(); err != nil {
			done <- fmt.Errorf("IMAP check failed: %w", err)
			return
		}
		if err := sess.VerifySMTP(); err != nil {
			done <- fmt.Errorf("SMTP check failed: %w", err)
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(verifyTimeout):
		return fmt.Errorf("verification timed out after %s (server unreachable?)", verifyTimeout)
	}
}
