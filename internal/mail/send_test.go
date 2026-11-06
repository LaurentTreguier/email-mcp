package mail

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"email-mcp/internal/config"
)

// fakeSMTP is a minimal SMTP sink that records submitted messages.
type fakeSMTP struct {
	ln      net.Listener
	gotFrom string
	gotRcpt []string
	gotData string
}

func startFakeSMTP(t *testing.T) (*fakeSMTP, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f, ln.Addr().String()
}

func (f *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	say := func(code int, msg string) {
		fmt.Fprintf(w, "%d %s\r\n", code, msg)
		w.Flush()
	}
	say(220, "fake ESMTP")
	inData := false
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if strings.TrimRight(line, "\r\n") == "." {
				inData = false
				f.gotData = data.String()
				say(250, "OK queued")
			} else {
				data.WriteString(line)
			}
			continue
		}
		cmd := strings.ToUpper(strings.Fields(line)[0])
		switch cmd {
		case "EHLO", "HELO":
			fmt.Fprintf(w, "250-fake\r\n250 8BITMIME\r\n")
			w.Flush()
		case "MAIL":
			f.gotFrom = line
			say(250, "OK")
		case "RCPT":
			f.gotRcpt = append(f.gotRcpt, line)
			say(250, "OK")
		case "DATA":
			say(354, "End with <CR><LF>.<CR><LF>")
			inData = true
		case "RSET":
			say(250, "OK")
		case "NOOP":
			say(250, "OK")
		case "QUIT":
			say(221, "Bye")
			return
		default:
			say(502, "not implemented")
		}
	}
}

func TestCompose(t *testing.T) {
	acc := &config.Account{Email: "user@example.com", DisplayName: "Test User"}
	raw, err := Compose(acc, &OutgoingMessage{
		To:        []string{"alice@example.org"},
		Cc:        []string{"bob@example.org"},
		Subject:   "Hi there",
		TextBody:  "Hello Alice!",
		InReplyTo: "msg1@example.org",
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	s := string(raw)
	for _, want := range []string{
		`From: "Test User" <user@example.com>`,
		"To: <alice@example.org>",
		"Cc: <bob@example.org>",
		"Subject: Hi there",
		"In-Reply-To: <msg1@example.org>",
		"References: <msg1@example.org>",
		"Message-Id:",
		"Hello Alice!",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("composed message missing %q:\n%s", want, s)
		}
	}
}

func TestComposeWithAttachment(t *testing.T) {
	acc := &config.Account{Email: "user@example.com"}
	raw, err := Compose(acc, &OutgoingMessage{
		To:       []string{"alice@example.org"},
		Subject:  "file",
		TextBody: "see attached",
		Attachments: []Attachment{{
			Filename:      "hello.txt",
			ContentType:   "text/plain",
			ContentBase64: "aGVsbG8gd29ybGQ=", // "hello world"
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "multipart/mixed") {
		t.Errorf("expected multipart message:\n%s", s)
	}
	if !strings.Contains(s, `filename="hello.txt"`) && !strings.Contains(s, "filename=hello.txt") {
		t.Errorf("attachment filename missing:\n%s", s)
	}
}

func TestSendViaFakeSMTP(t *testing.T) {
	b := newFakeBackend()
	imapAddr := startFakeIMAP(t, b)
	f, smtpAddr := startFakeSMTP(t)

	acc := &config.Account{
		Email:    "user@example.com",
		Username: "user@example.com",
		Password: "secret",
		IMAP:     config.IMAPConfig{Host: hostOf(imapAddr), Port: portOf(imapAddr), Insecure: true},
		SMTP:     config.SMTPConfig{Host: hostOf(smtpAddr), Port: portOf(smtpAddr), Insecure: true},
	}
	s := NewSession(acc)
	defer s.Close()

	raw, err := Compose(acc, &OutgoingMessage{
		To:       []string{"alice@example.org"},
		Subject:  "test send",
		TextBody: "body text",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(raw, acc.Email, []string{"alice@example.org"}, true); err != nil {
		t.Fatalf("Send: %v", err)
	}
	t.Logf("backend Sent messages after send: %d", len(b.mailboxes["Sent"]))
	if f.gotData == "" {
		t.Fatal("fake SMTP received no data")
	}
	if !strings.Contains(f.gotData, "Subject: test send") {
		t.Errorf("submitted message missing subject:\n%s", f.gotData)
	}
	if len(f.gotRcpt) != 1 {
		t.Errorf("want 1 RCPT, got %d", len(f.gotRcpt))
	}
	// Sent copy should have been appended to Sent.
	st, err := s.MailboxStatus("Sent")
	if err != nil || st.NumMessages == nil || *st.NumMessages != 1 {
		t.Errorf("Sent mailbox should contain 1 copy: %+v err=%v", st, err)
	}
}

var _ = io.EOF
