package mail

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"

	"email-mcp/internal/config"
)

// fakeMessage is one stored message.
type fakeMessage struct {
	uid   imap.UID
	flags []imap.Flag
	raw   string
}

// fakeBackend is a minimal in-memory IMAP4rev1 backend for tests.
type fakeBackend struct {
	mu         sync.Mutex
	mailboxes  map[string][]*fakeMessage
	subscribed map[string]bool
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		mailboxes:  map[string][]*fakeMessage{"INBOX": {}, "Sent": {}, "Drafts": {}, "Archive": {}},
		subscribed: map[string]bool{"INBOX": true},
	}
}

func (b *fakeBackend) addMessage(mailbox, raw string) imap.UID {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.mailboxes[mailbox]; !ok {
		b.mailboxes[mailbox] = nil
	}
	msgs := b.mailboxes[mailbox]
	uid := imap.UID(len(msgs) + 1)
	b.mailboxes[mailbox] = append(msgs, &fakeMessage{uid: uid, raw: raw})
	return uid
}

type fakeSession struct {
	b        *fakeBackend
	current  string // selected mailbox, "" if none
	readOnly bool
	t        *testing.T
}

func (s *fakeSession) Close() error { return nil }

func (s *fakeSession) Login(username, password string) error {
	if username == "user@example.com" && password == "secret" {
		return nil
	}
	return fmt.Errorf("authentication failed")
}

func (s *fakeSession) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	msgs, ok := s.b.mailboxes[mailbox]
	if !ok {
		return nil, fmt.Errorf("mailbox does not exist")
	}
	s.current = mailbox
	s.readOnly = options != nil && options.ReadOnly
	return &imap.SelectData{
		Flags:       []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft},
		NumMessages: uint32(len(msgs)),
		UIDNext:     imap.UID(len(msgs) + 1),
		UIDValidity: 1,
	}, nil
}

func (s *fakeSession) Unselect() error { s.current = ""; return nil }

func (s *fakeSession) Create(mailbox string, options *imap.CreateOptions) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if _, ok := s.b.mailboxes[mailbox]; ok {
		return fmt.Errorf("mailbox exists")
	}
	s.b.mailboxes[mailbox] = nil
	return nil
}

func (s *fakeSession) Delete(mailbox string) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	delete(s.b.mailboxes, mailbox)
	return nil
}

func (s *fakeSession) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.mailboxes[newName] = s.b.mailboxes[mailbox]
	delete(s.b.mailboxes, mailbox)
	return nil
}

func (s *fakeSession) Subscribe(mailbox string) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.subscribed[mailbox] = true
	return nil
}

func (s *fakeSession) Unsubscribe(mailbox string) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	delete(s.b.subscribed, mailbox)
	return nil
}

func (s *fakeSession) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	for name, msgs := range s.b.mailboxes {
		if options != nil && options.SelectSubscribed && !s.b.subscribed[name] {
			continue
		}
		attrs := []imap.MailboxAttr{}
		if s.b.subscribed[name] {
			attrs = append(attrs, imap.MailboxAttrSubscribed)
		}
		data := &imap.ListData{Attrs: attrs, Delim: '/', Mailbox: name}
		if options != nil && options.ReturnStatus != nil {
			n := uint32(len(msgs))
			unseen := uint32(0)
			for _, m := range msgs {
				hasSeen := false
				for _, f := range m.flags {
					if f == imap.FlagSeen {
						hasSeen = true
					}
				}
				if !hasSeen {
					unseen++
				}
			}
			data.Status = &imap.StatusData{
				Mailbox:     name,
				NumMessages: &n,
				NumUnseen:   &unseen,
				UIDNext:     imap.UID(len(msgs) + 1),
				UIDValidity: 1,
			}
		}
		if err := w.WriteList(data); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeSession) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	msgs, ok := s.b.mailboxes[mailbox]
	if !ok {
		return nil, fmt.Errorf("no such mailbox")
	}
	t := s.t
	if t == nil {
		t = &testing.T{}
	}
	t.Logf("STATUS %s: %d messages in backend", mailbox, len(msgs))
	n := uint32(len(msgs))
	unseen := uint32(0)
	deleted := uint32(0)
	var size int64
	for _, m := range msgs {
		size += int64(len(m.raw))
		hasSeen := false
		for _, f := range m.flags {
			if f == imap.FlagSeen {
				hasSeen = true
			}
			if f == imap.FlagDeleted {
				deleted++
			}
		}
		if !hasSeen {
			unseen++
		}
	}
	return &imap.StatusData{
		Mailbox:     mailbox,
		NumMessages: &n,
		NumUnseen:   &unseen,
		NumDeleted:  &deleted,
		UIDNext:     imap.UID(len(msgs) + 1),
		UIDValidity: 1,
		Size:        &size,
	}, nil
}

func (s *fakeSession) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	raw, _ := io.ReadAll(r)
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if _, ok := s.b.mailboxes[mailbox]; !ok {
		s.b.mailboxes[mailbox] = nil
	}
	msgs := s.b.mailboxes[mailbox]
	uid := imap.UID(len(msgs) + 1)
	m := &fakeMessage{uid: uid, raw: string(raw)}
	if options != nil {
		m.flags = options.Flags
	}
	s.b.mailboxes[mailbox] = append(msgs, m)
	if s.t != nil {
		s.t.Logf("APPEND to %s: now %d messages (uid=%d)", mailbox, len(s.b.mailboxes[mailbox]), uid)
	}
	return &imap.AppendData{UID: uid}, nil
}

func (s *fakeSession) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error { return nil }
func (s *fakeSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	<-stop
	return nil
}

func (s *fakeSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	msgs := s.b.mailboxes[s.current]
	var kept []*fakeMessage
	for i, m := range msgs {
		deleted := false
		for _, f := range m.flags {
			if f == imap.FlagDeleted {
				deleted = true
			}
		}
		if uids != nil && !uids.Contains(m.uid) {
			deleted = false
		}
		if deleted {
			w.WriteExpunge(uint32(i + 1))
		} else {
			kept = append(kept, m)
		}
	}
	s.b.mailboxes[s.current] = kept
	return nil
}

func (s *fakeSession) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	msgs := s.b.mailboxes[s.current]
	set := imap.UIDSetNum()
	for _, m := range msgs {
		if matchCriteria(m.raw, m.flags, criteria) {
			set.AddNum(m.uid)
		}
	}
	return &imap.SearchData{All: set}, nil
}

func matchCriteria(raw string, flags []imap.Flag, c *imap.SearchCriteria) bool {
	lower := strings.ToLower(raw)
	for _, h := range c.Header {
		needle := strings.ToLower(h.Key + ": " + h.Value)
		if !strings.Contains(lower, needle) {
			return false
		}
	}
	for _, b := range c.Body {
		if !strings.Contains(lower, strings.ToLower(b)) {
			return false
		}
	}
	for _, t := range c.Text {
		if !strings.Contains(lower, strings.ToLower(t)) {
			return false
		}
	}
	hasFlag := func(f imap.Flag) bool {
		for _, mf := range flags {
			if mf == f {
				return true
			}
		}
		return false
	}
	for _, f := range c.Flag {
		if !hasFlag(f) {
			return false
		}
	}
	for _, nf := range c.NotFlag {
		if hasFlag(nf) {
			return false
		}
	}
	return true
}

func (s *fakeSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	msgs := s.b.mailboxes[s.current]
	for i, m := range msgs {
		match := false
		if uidSet, ok := numSet.(imap.UIDSet); ok {
			match = uidSet.Contains(m.uid)
		} else if seqSet, ok := numSet.(imap.SeqSet); ok {
			match = seqSet.Contains(uint32(i + 1))
		}
		if !match {
			continue
		}
		fw := w.CreateMessage(uint32(i + 1))
		if options.UID || numSet != nil {
			fw.WriteUID(m.uid)
		}
		if options.Flags {
			fw.WriteFlags(m.flags)
		}
		if options.InternalDate {
			fw.WriteInternalDate(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		}
		if options.RFC822Size {
			fw.WriteRFC822Size(int64(len(m.raw)))
		}
		if options.Envelope {
			head, _, _ := strings.Cut(m.raw, "\n\n")
			h, err := textproto.ReadHeader(bufio.NewReader(strings.NewReader(head + "\n\n")))
			if err != nil {
				return err
			}
			fw.WriteEnvelope(imapserver.ExtractEnvelope(h))
		}
		if options.BodyStructure != nil {
			fw.WriteBodyStructure(imapserver.ExtractBodyStructure(strings.NewReader(m.raw)))
		}
		for _, section := range options.BodySection {
			data := imapserver.ExtractBodySection(strings.NewReader(m.raw), section)
			wc := fw.WriteBodySection(section, int64(len(data)))
			wc.Write(data)
			wc.Close()
		}
		fw.Close()
	}
	return nil
}

func (s *fakeSession) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	msgs := s.b.mailboxes[s.current]
	for _, m := range msgs {
		if !numSet.(imap.UIDSet).Contains(m.uid) {
			continue
		}
		switch flags.Op {
		case imap.StoreFlagsAdd:
			m.flags = append(m.flags, flags.Flags...)
		case imap.StoreFlagsDel:
			var kept []imap.Flag
			for _, f := range m.flags {
				remove := false
				for _, rf := range flags.Flags {
					if f == rf {
						remove = true
					}
				}
				if !remove {
					kept = append(kept, f)
				}
			}
			m.flags = kept
		case imap.StoreFlagsSet:
			m.flags = flags.Flags
		}
	}
	return nil
}

func (s *fakeSession) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if _, ok := s.b.mailboxes[dest]; !ok {
		return nil, fmt.Errorf("no such mailbox")
	}
	src := imap.UIDSetNum()
	dst := imap.UIDSetNum()
	for _, m := range s.b.mailboxes[s.current] {
		if numSet.(imap.UIDSet).Contains(m.uid) {
			newUID := imap.UID(len(s.b.mailboxes[dest]) + 1)
			s.b.mailboxes[dest] = append(s.b.mailboxes[dest], &fakeMessage{
				uid:   newUID,
				flags: m.flags,
				raw:   m.raw,
			})
			src.AddNum(m.uid)
			dst.AddNum(newUID)
		}
	}
	return &imap.CopyData{UIDValidity: 1, SourceUIDs: src, DestUIDs: dst}, nil
}

// Ensure the imapclient import is used even if tests change.
var _ = imapclient.DialInsecure

// startFakeIMAP starts an in-memory IMAP server and returns its address.
func startFakeIMAP(t *testing.T, b *fakeBackend) string {
	t.Helper()
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &fakeSession{b: b, t: t}, &imapserver.GreetingData{}, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
		Logger:       testLogger{t},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close(); ln.Close() })
	return ln.Addr().String()
}

func testSession(t *testing.T, addr string) *Session {
	t.Helper()
	acc := &config.Account{
		Email:    "user@example.com",
		Username: "user@example.com",
		Password: "secret",
		IMAP:     config.IMAPConfig{Host: hostOf(addr), Port: portOf(addr), Insecure: true},
	}
	s := NewSession(acc)
	t.Cleanup(s.Close)
	return s
}

func hostOf(addr string) string {
	h, _, _ := strings.Cut(addr, ":")
	return h
}

func portOf(addr string) int {
	_, p, _ := strings.Cut(addr, ":")
	var port int
	fmt.Sscanf(p, "%d", &port)
	return port
}

const sampleMessage = "From: Alice <alice@example.org>\r\n" +
	"To: user@example.com\r\n" +
	"Subject: Hello there\r\n" +
	"Message-ID: <msg1@example.org>\r\n" +
	"Date: Thu, 01 Jan 2026 10:00:00 +0000\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"This is the body of the hello message.\r\n"

func TestVerifyIMAP(t *testing.T) {
	b := newFakeBackend()
	b.addMessage("INBOX", sampleMessage)
	addr := startFakeIMAP(t, b)
	s := testSession(t, addr)
	if err := s.VerifyIMAP(); err != nil {
		t.Fatalf("VerifyIMAP: %v", err)
	}
}

func TestVerifyIMAPBadPassword(t *testing.T) {
	b := newFakeBackend()
	addr := startFakeIMAP(t, b)
	acc := &config.Account{
		Email:    "user@example.com",
		Username: "user@example.com",
		Password: "wrong",
		IMAP:     config.IMAPConfig{Host: hostOf(addr), Port: portOf(addr), Insecure: true},
	}
	s := NewSession(acc)
	defer s.Close()
	if err := s.VerifyIMAP(); err == nil {
		t.Fatal("expected error for wrong password")
	}
}

// strictRev1Session mimics an IMAP4rev1-only server (e.g. Dovecot without
// the QUOTA capability) that rejects the whole STATUS command with BAD when
// a non-rev1 status item (DELETED, SIZE) is requested.
type strictRev1Session struct{ *fakeSession }

func (s *strictRev1Session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	if options.NumDeleted || options.Size {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeBad,
			Text: "Invalid status item DELETED",
		}
	}
	return s.fakeSession.Status(mailbox, options)
}

// startStrictRev1IMAP starts an in-memory IMAP4rev1 server that rejects
// DELETED/SIZE status items, like the user's server does.
func startStrictRev1IMAP(t *testing.T, b *fakeBackend) string {
	t.Helper()
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &strictRev1Session{&fakeSession{b: b, t: t}}, &imapserver.GreetingData{}, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
		Logger:       testLogger{t},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close(); ln.Close() })
	return ln.Addr().String()
}

func TestVerifyIMAPStrictRev1Server(t *testing.T) {
	b := newFakeBackend()
	b.addMessage("INBOX", sampleMessage)
	addr := startStrictRev1IMAP(t, b)
	s := testSession(t, addr)
	if err := s.VerifyIMAP(); err != nil {
		t.Fatalf("VerifyIMAP against strict IMAP4rev1 server: %v", err)
	}
	st, err := s.MailboxStatus("INBOX")
	if err != nil {
		t.Fatalf("MailboxStatus against strict IMAP4rev1 server: %v", err)
	}
	if st.NumMessages == nil || *st.NumMessages != 1 {
		t.Fatalf("unexpected status data: %+v", st)
	}
}

func TestListAndStatus(t *testing.T) {
	b := newFakeBackend()
	b.addMessage("INBOX", sampleMessage)
	addr := startFakeIMAP(t, b)
	s := testSession(t, addr)

	boxes, err := s.ListMailboxes("*", false, true)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	var inbox *MailboxInfo
	for i := range boxes {
		if boxes[i].Name == "INBOX" {
			inbox = &boxes[i]
		}
	}
	if inbox == nil {
		t.Fatalf("INBOX not found in mailboxes: %+v", boxes)
	}
	if inbox.NumMessages == nil || *inbox.NumMessages != 1 {
		t.Fatalf("unexpected status: %+v", inbox)
	}

	st, err := s.MailboxStatus("INBOX")
	if err != nil {
		t.Fatalf("MailboxStatus: %v", err)
	}
	if st.NumMessages == nil || *st.NumMessages != 1 || *st.NumUnseen != 1 {
		t.Fatalf("unexpected status data: %+v", st)
	}
}

func TestSearchListGetMessage(t *testing.T) {
	b := newFakeBackend()
	uid1 := b.addMessage("INBOX", sampleMessage)
	b.addMessage("INBOX", strings.ReplaceAll(sampleMessage, "Hello there", "Second message"))
	addr := startFakeIMAP(t, b)
	s := testSession(t, addr)

	uids, err := s.SearchMessages("INBOX", &SearchQuery{Subject: "Hello"})
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(uids) != 1 || uids[0] != uint32(uid1) {
		t.Fatalf("unexpected search results: %v (want [%d])", uids, uid1)
	}

	msgs, err := s.ListMessages("INBOX", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].Subject != "Hello there" {
		t.Fatalf("unexpected subject: %q", msgs[0].Subject)
	}

	msg, err := s.GetMessage("INBOX", uint32(uid1))
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg.Subject != "Hello there" {
		t.Fatalf("unexpected message subject: %q", msg.Subject)
	}
	if !strings.Contains(msg.TextBody, "body of the hello message") {
		t.Fatalf("unexpected text body: %q", msg.TextBody)
	}
	if len(msg.From) != 1 || msg.From[0].Email != "alice@example.org" {
		t.Fatalf("unexpected from: %+v", msg.From)
	}
}

func TestFlagsMoveCopyDelete(t *testing.T) {
	b := newFakeBackend()
	uid1 := b.addMessage("INBOX", sampleMessage)
	b.addMessage("INBOX", strings.ReplaceAll(sampleMessage, "Hello", "World"))
	b.mailboxes["Archive"] = nil
	addr := startFakeIMAP(t, b)
	s := testSession(t, addr)

	if err := s.SetFlags("INBOX", []uint32{uint32(uid1)}, nil, "add", []string{"\\Seen", "\\Flagged"}); err != nil {
		t.Fatalf("SetFlags: %v", err)
	}
	msg, err := s.GetMessage("INBOX", uint32(uid1))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(msg.Flags, ",")
	if !strings.Contains(joined, "\\Seen") || !strings.Contains(joined, "\\Flagged") {
		t.Fatalf("flags not set: %v", msg.Flags)
	}

	if err := s.CopyMessages("INBOX", "Archive", []uint32{uint32(uid1)}, nil); err != nil {
		t.Fatalf("CopyMessages: %v", err)
	}
	st, _ := s.MailboxStatus("Archive")
	if *st.NumMessages != 1 {
		t.Fatalf("copy failed, Archive has %d messages", *st.NumMessages)
	}

	if err := s.MoveMessages("INBOX", "Archive", []uint32{uint32(uid1)}, nil); err != nil {
		t.Fatalf("MoveMessages: %v", err)
	}
	st, _ = s.MailboxStatus("INBOX")
	if *st.NumMessages != 1 {
		t.Fatalf("move failed, INBOX has %d messages", *st.NumMessages)
	}

	if err := s.DeleteMessages("INBOX", nil, []string{"1:*"}, true); err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	st, _ = s.MailboxStatus("INBOX")
	if *st.NumMessages != 0 {
		t.Fatalf("delete failed, INBOX has %d messages", *st.NumMessages)
	}
}

func TestMailboxCRUD(t *testing.T) {
	b := newFakeBackend()
	addr := startFakeIMAP(t, b)
	s := testSession(t, addr)

	if err := s.CreateMailbox("Projects"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	if err := s.Subscribe("Projects"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	subs, err := s.ListMailboxes("*", true, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mb := range subs {
		if mb.Name == "Projects" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Projects not in subscribed list: %+v", subs)
	}
	if err := s.RenameMailbox("Projects", "Work"); err != nil {
		t.Fatalf("RenameMailbox: %v", err)
	}
	if err := s.DeleteMailbox("Work"); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	boxes, _ := s.ListMailboxes("*", false, false)
	for _, mb := range boxes {
		if mb.Name == "Work" {
			t.Fatalf("Work should be deleted")
		}
	}
}

func TestAppend(t *testing.T) {
	b := newFakeBackend()
	addr := startFakeIMAP(t, b)
	s := testSession(t, addr)

	if err := s.AppendMessage("INBOX", []byte(sampleMessage), []string{"\\Seen"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	st, err := s.MailboxStatus("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if *st.NumMessages != 1 {
		t.Fatalf("append failed: %+v", st)
	}
	msg, err := s.GetMessage("INBOX", 1)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "Hello there" {
		t.Fatalf("unexpected appended message: %+v", msg)
	}
}

// Ensure the imapclient import is used even if tests change.
var _ = imapclient.DialInsecure

type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) {
	if l.t != nil {
		l.t.Logf("imapserver: "+format, args...)
	}
}
