package mail

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"email-mcp/internal/config"
)

// Attachment is a file to attach to an outgoing message.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"` // defaults to application/octet-stream
	// ContentBase64 is the base64-encoded file content.
	ContentBase64 string `json:"content_base64"`
}

// OutgoingMessage describes an email to send or save as draft.
type OutgoingMessage struct {
	To          []string     `json:"to"`
	Cc          []string     `json:"cc,omitempty"`
	Bcc         []string     `json:"bcc,omitempty"`
	Subject     string       `json:"subject"`
	TextBody    string       `json:"text_body,omitempty"`
	HTMLBody    string       `json:"html_body,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	// InReplyTo is the Message-ID of the message being replied to (without angle brackets).
	InReplyTo string `json:"in_reply_to,omitempty"`
	// References are Message-IDs for threading (without angle brackets).
	References []string `json:"references,omitempty"`
}

// Compose builds a raw RFC 5322 message from the account identity and the outgoing message.
func Compose(acc *config.Account, msg *OutgoingMessage) ([]byte, error) {
	if len(msg.To) == 0 && len(msg.Cc) == 0 && len(msg.Bcc) == 0 {
		return nil, fmt.Errorf("at least one recipient (to, cc or bcc) is required")
	}
	if msg.TextBody == "" && msg.HTMLBody == "" {
		return nil, fmt.Errorf("a text_body or html_body is required")
	}

	var h mail.Header
	h.SetDate(nowFunc())
	if err := h.GenerateMessageID(); err != nil {
		return nil, err
	}
	from := &mail.Address{Name: acc.DisplayName, Address: acc.Email}
	h.SetAddressList("From", []*mail.Address{from})
	if err := setAddrList(&h, "To", msg.To); err != nil {
		return nil, err
	}
	if len(msg.Cc) > 0 {
		if err := setAddrList(&h, "Cc", msg.Cc); err != nil {
			return nil, err
		}
	}
	h.SetSubject(msg.Subject)
	if msg.InReplyTo != "" {
		h.SetMsgIDList("In-Reply-To", []string{msg.InReplyTo})
	}
	refs := msg.References
	if msg.InReplyTo != "" && !containsStr(refs, msg.InReplyTo) {
		refs = append(refs, msg.InReplyTo)
	}
	if len(refs) > 0 {
		h.SetMsgIDList("References", refs)
	}

	var buf bytes.Buffer
	w, err := mail.CreateWriter(&buf, h)
	if err != nil {
		return nil, err
	}
	if msg.TextBody != "" {
		iw, err := w.CreateInline()
		if err != nil {
			return nil, err
		}
		ih := mail.InlineHeader{}
		ih.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		pw, err := iw.CreatePart(ih)
		if err != nil {
			return nil, err
		}
		if _, err := pw.Write([]byte(msg.TextBody)); err != nil {
			return nil, err
		}
		if err := pw.Close(); err != nil {
			return nil, err
		}
		if err := iw.Close(); err != nil {
			return nil, err
		}
	}
	if msg.HTMLBody != "" {
		iw, err := w.CreateInline()
		if err != nil {
			return nil, err
		}
		ih := mail.InlineHeader{}
		ih.SetContentType("text/html", map[string]string{"charset": "utf-8"})
		pw, err := iw.CreatePart(ih)
		if err != nil {
			return nil, err
		}
		if _, err := pw.Write([]byte(msg.HTMLBody)); err != nil {
			return nil, err
		}
		if err := pw.Close(); err != nil {
			return nil, err
		}
		if err := iw.Close(); err != nil {
			return nil, err
		}
	}
	for _, att := range msg.Attachments {
		decoded, err := decodeBase64(att.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %w", att.Filename, err)
		}
		ah := mail.AttachmentHeader{}
		ct := att.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		ah.SetContentType(ct, nil)
		ah.SetFilename(att.Filename)
		aw, err := w.CreateAttachment(ah)
		if err != nil {
			return nil, err
		}
		if _, err := aw.Write(decoded); err != nil {
			return nil, err
		}
		if err := aw.Close(); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func setAddrList(h *mail.Header, key string, addrs []string) error {
	list, err := mail.ParseAddressList(strings.Join(addrs, ", "))
	if err != nil {
		return fmt.Errorf("invalid %s address: %w", key, err)
	}
	h.SetAddressList(key, list)
	return nil
}

func containsStr(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

// Send submits a raw message via SMTP and, on success, appends it to the Sent mailbox.
// recipients is the full envelope recipient list (to + cc + bcc).
// saveToSent controls whether a copy is appended to the Sent mailbox.
func (s *Session) Send(raw []byte, from string, recipients []string, saveToSent bool) error {
	acc := s.acc
	addr := net.JoinHostPort(acc.SMTP.Host, strconv.Itoa(acc.SMTP.Port))
	tlsCfg := &tls.Config{ServerName: acc.SMTP.Host, InsecureSkipVerify: acc.SMTP.InsecureSkipVerify} //nolint:gosec // opt-in for testing

	password := acc.Password

	var c *smtp.Client
	var err error
	switch {
	case acc.SMTP.TLS:
		c, err = smtp.DialTLS(addr, tlsCfg)
	case acc.SMTP.Insecure:
		c, err = smtp.Dial(addr)
	default:
		c, err = smtp.DialStartTLS(addr, tlsCfg)
	}
	if err != nil {
		return fmt.Errorf("dialing SMTP %s: %w", addr, err)
	}
	defer c.Close()

	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("SMTP EHLO: %w", err)
	}
	switch acc.AuthMechanism {
	case "", "plain":
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(sasl.NewPlainClient("", acc.Username, password)); err != nil {
				return fmt.Errorf("SMTP AUTH PLAIN: %w", err)
			}
		}
	case "login":
		if err := c.Auth(sasl.NewLoginClient(acc.Username, password)); err != nil {
			return fmt.Errorf("SMTP AUTH LOGIN: %w", err)
		}
	case "xoauth2":
		if err := c.Auth(sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
			Username: acc.Username,
			Token:    password,
		})); err != nil {
			return fmt.Errorf("SMTP AUTH XOAUTH2: %w", err)
		}
	default:
		return fmt.Errorf("unsupported auth_mechanism %q", acc.AuthMechanism)
	}

	if err := c.Mail(from, &smtp.MailOptions{Body: smtp.Body8BitMIME}); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	for _, rcpt := range recipients {
		if err := c.Rcpt(rcpt, nil); err != nil {
			return fmt.Errorf("SMTP RCPT TO %s: %w", rcpt, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := wc.Write(raw); err != nil {
		wc.Close()
		return fmt.Errorf("SMTP writing message: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("SMTP message rejected: %w", err)
	}
	if err := c.Quit(); err != nil {
		// Message was accepted; a QUIT error is not fatal.
		_ = err
	}

	// Save a copy to the Sent mailbox.
	if saveToSent {
		if err := s.AppendMessage(acc.SentMailbox, raw, []string{imapFlagSeen}); err != nil {
			return fmt.Errorf("message sent via SMTP, but saving to %s failed: %w", acc.SentMailbox, err)
		}
	}
	return nil
}

const imapFlagSeen = "\\Seen"
