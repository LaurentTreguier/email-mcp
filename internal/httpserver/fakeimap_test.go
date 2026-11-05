package httpserver

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// minimal IMAP backend good enough for list_mailboxes + mailboxes resource.
type miniBackend struct {
	mu sync.Mutex
}

type miniSession struct{ b *miniBackend }

func (s *miniSession) Close() error { return nil }
func (s *miniSession) Login(u, p string) error {
	if u == "user@example.com" && p == "secret" {
		return nil
	}
	return imapserver.ErrAuthFailed
}
func (s *miniSession) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	return &imap.SelectData{NumMessages: 0, UIDNext: 1, UIDValidity: 1}, nil
}
func (s *miniSession) Unselect() error                                    { return nil }
func (s *miniSession) Create(mailbox string, o *imap.CreateOptions) error { return nil }
func (s *miniSession) Delete(mailbox string) error                        { return nil }
func (s *miniSession) Rename(m, n string, o *imap.RenameOptions) error    { return nil }
func (s *miniSession) Subscribe(mailbox string) error                     { return nil }
func (s *miniSession) Unsubscribe(mailbox string) error                   { return nil }
func (s *miniSession) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	return w.WriteList(&imap.ListData{Attrs: []imap.MailboxAttr{imap.MailboxAttrSubscribed}, Delim: '/', Mailbox: "INBOX"})
}
func (s *miniSession) Status(mailbox string, o *imap.StatusOptions) (*imap.StatusData, error) {
	n := uint32(0)
	return &imap.StatusData{Mailbox: mailbox, NumMessages: &n, UIDNext: 1, UIDValidity: 1}, nil
}
func (s *miniSession) Append(mailbox string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	return &imap.AppendData{UID: 1}, nil
}
func (s *miniSession) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error { return nil }
func (s *miniSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	<-stop
	return nil
}
func (s *miniSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error { return nil }
func (s *miniSession) Search(kind imapserver.NumKind, c *imap.SearchCriteria, o *imap.SearchOptions) (*imap.SearchData, error) {
	return &imap.SearchData{All: imap.UIDSetNum()}, nil
}
func (s *miniSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, o *imap.FetchOptions) error {
	return nil
}
func (s *miniSession) Store(w *imapserver.FetchWriter, numSet imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	return nil
}
func (s *miniSession) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	return &imap.CopyData{UIDValidity: 1, SourceUIDs: imap.UIDSetNum(), DestUIDs: imap.UIDSetNum()}, nil
}

func init() {
	startFakeIMAP = func(t *testing.T) (string, func()) {
		b := &miniBackend{}
		srv := imapserver.New(&imapserver.Options{
			NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
				return &miniSession{b: b}, &imapserver.GreetingData{}, nil
			},
			Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
			InsecureAuth: true,
		})
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go srv.Serve(ln) //nolint:errcheck
		// ensure the port is usable
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			c, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond)
			if err == nil {
				c.Close()
				break
			}
		}
		return ln.Addr().String(), func() { srv.Close(); ln.Close() }
	}
}

var _ = strings.TrimSpace
