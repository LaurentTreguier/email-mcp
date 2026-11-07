package mail

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// MailboxInfo describes a mailbox returned by LIST.
type MailboxInfo struct {
	Name        string   `json:"name"`
	Delimiter   string   `json:"delimiter,omitempty"`
	Attributes  []string `json:"attributes,omitempty"`
	Subscribed  bool     `json:"subscribed,omitempty"`
	NumMessages *uint32  `json:"num_messages,omitempty"`
	NumUnseen   *uint32  `json:"num_unseen,omitempty"`
	UIDNext     uint32   `json:"uid_next,omitempty"`
	UIDValidity uint32   `json:"uid_validity,omitempty"`
}

// MailboxStatus is the result of a STATUS command.
type MailboxStatus struct {
	Mailbox     string  `json:"mailbox"`
	NumMessages *uint32 `json:"num_messages,omitempty"`
	NumUnseen   *uint32 `json:"num_unseen,omitempty"`
	NumDeleted  *uint32 `json:"num_deleted,omitempty"`
	UIDNext     uint32  `json:"uid_next,omitempty"`
	UIDValidity uint32  `json:"uid_validity,omitempty"`
	Size        *int64  `json:"size,omitempty"`
}

// VerifyIMAP checks that the IMAP server is reachable and the credentials
// are accepted, using only read-only commands: dial + LOGIN/AUTHENTICATE,
// LIST "" "*", STATUS INBOX. Used to validate an account at setup time.
func (s *Session) VerifyIMAP() error {
	if _, err := s.ListMailboxes("*", false, false); err != nil {
		return err
	}
	if _, err := s.MailboxStatus("INBOX"); err != nil {
		return fmt.Errorf("STATUS INBOX: %w", err)
	}
	return nil
}

// Address is a simplified mail address.
type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

func addrPtr(a *imap.Address) Address {
	if a == nil {
		return Address{}
	}
	return Address{Name: a.Name, Email: a.Addr()}
}

func addrList(l []imap.Address) []Address {
	out := make([]Address, 0, len(l))
	for i := range l {
		out = append(out, addrPtr(&l[i]))
	}
	return out
}

// MessageSummary is a message listing entry (envelope + flags).
type MessageSummary struct {
	UID          uint32    `json:"uid"`
	SeqNum       uint32    `json:"seq_num"`
	Flags        []string  `json:"flags,omitempty"`
	InternalDate time.Time `json:"internal_date,omitempty"`
	Size         int64     `json:"size,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	From         []Address `json:"from,omitempty"`
	To           []Address `json:"to,omitempty"`
	Cc           []Address `json:"cc,omitempty"`
	MessageID    string    `json:"message_id,omitempty"`
}

// AttachmentInfo describes one MIME part of a message.
type AttachmentInfo struct {
	PartID      string `json:"part_id"` // e.g. "2" or "1.2"; empty for the root part
	MediaType   string `json:"media_type"`
	Filename    string `json:"filename,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// Message is a fully fetched message.
type Message struct {
	UID          uint32            `json:"uid"`
	SeqNum       uint32            `json:"seq_num"`
	Flags        []string          `json:"flags,omitempty"`
	InternalDate time.Time         `json:"internal_date,omitempty"`
	Size         int64             `json:"size,omitempty"`
	Subject      string            `json:"subject,omitempty"`
	From         []Address         `json:"from,omitempty"`
	To           []Address         `json:"to,omitempty"`
	Cc           []Address         `json:"cc,omitempty"`
	Bcc          []Address         `json:"bcc,omitempty"`
	ReplyTo      []Address         `json:"reply_to,omitempty"`
	MessageID    string            `json:"message_id,omitempty"`
	InReplyTo    []string          `json:"in_reply_to,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	TextBody     string            `json:"text_body,omitempty"`
	HTMLBody     string            `json:"html_body,omitempty"`
	Attachments  []AttachmentInfo  `json:"attachments,omitempty"`
}

// SearchQuery is a simplified search request mapped onto IMAP SEARCH criteria.
type SearchQuery struct {
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	Cc         string   `json:"cc,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	Body       string   `json:"body,omitempty"`
	Text       string   `json:"text,omitempty"`
	Since      string   `json:"since,omitempty"`  // YYYY-MM-DD
	Before     string   `json:"before,omitempty"` // YYYY-MM-DD
	Seen       *bool    `json:"seen,omitempty"`
	Flagged    *bool    `json:"flagged,omitempty"`
	Answered   *bool    `json:"answered,omitempty"`
	Draft      *bool    `json:"draft,omitempty"`
	Deleted    *bool    `json:"deleted,omitempty"`
	Larger     *int64   `json:"larger,omitempty"`
	Smaller    *int64   `json:"smaller,omitempty"`
	UIDs       []uint32 `json:"uids,omitempty"`
	NotSubject string   `json:"not_subject,omitempty"`
}

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", s)
	}
	return t, nil
}

func (q *SearchQuery) criteria() (*imap.SearchCriteria, error) {
	c := &imap.SearchCriteria{}
	if q.From != "" {
		c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: q.From})
	}
	if q.To != "" {
		c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: q.To})
	}
	if q.Cc != "" {
		c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "Cc", Value: q.Cc})
	}
	if q.Subject != "" {
		c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: q.Subject})
	}
	if q.NotSubject != "" {
		c.Not = append(c.Not, imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: q.NotSubject}}})
	}
	if q.Body != "" {
		c.Body = []string{q.Body}
	}
	if q.Text != "" {
		c.Text = []string{q.Text}
	}
	if q.Since != "" {
		t, err := parseDate(q.Since)
		if err != nil {
			return nil, err
		}
		c.Since = t
	}
	if q.Before != "" {
		t, err := parseDate(q.Before)
		if err != nil {
			return nil, err
		}
		c.Before = t
	}
	if q.Seen != nil {
		if *q.Seen {
			c.Flag = append(c.Flag, imap.FlagSeen)
		} else {
			c.NotFlag = append(c.NotFlag, imap.FlagSeen)
		}
	}
	if q.Flagged != nil {
		if *q.Flagged {
			c.Flag = append(c.Flag, imap.FlagFlagged)
		} else {
			c.NotFlag = append(c.NotFlag, imap.FlagFlagged)
		}
	}
	if q.Answered != nil {
		if *q.Answered {
			c.Flag = append(c.Flag, imap.FlagAnswered)
		} else {
			c.NotFlag = append(c.NotFlag, imap.FlagAnswered)
		}
	}
	if q.Draft != nil {
		if *q.Draft {
			c.Flag = append(c.Flag, imap.FlagDraft)
		} else {
			c.NotFlag = append(c.NotFlag, imap.FlagDraft)
		}
	}
	if q.Deleted != nil {
		if *q.Deleted {
			c.Flag = append(c.Flag, imap.FlagDeleted)
		} else {
			c.NotFlag = append(c.NotFlag, imap.FlagDeleted)
		}
	}
	if q.Larger != nil {
		c.Larger = *q.Larger
	}
	if q.Smaller != nil {
		c.Smaller = *q.Smaller
	}
	if len(q.UIDs) > 0 {
		set := imap.UIDSetNum()
		for _, u := range q.UIDs {
			set.AddNum(imap.UID(u))
		}
		c.UID = []imap.UIDSet{set}
	}
	return c, nil
}

// uidSet builds a UIDSet from individual UIDs and "start:stop" range strings.
func uidSet(uids []uint32, ranges []string) (imap.UIDSet, error) {
	set := imap.UIDSetNum()
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	for _, r := range ranges {
		start, stop, ok := strings.Cut(r, ":")
		if !ok {
			return nil, fmt.Errorf("invalid UID range %q (want start:stop)", r)
		}
		var a, b uint32
		if _, err := fmt.Sscanf(start, "%d", &a); err != nil {
			return nil, fmt.Errorf("invalid UID range %q", r)
		}
		if stop == "*" {
			b = ^uint32(0)
		} else if _, err := fmt.Sscanf(stop, "%d", &b); err != nil {
			return nil, fmt.Errorf("invalid UID range %q", r)
		}
		set.AddRange(imap.UID(a), imap.UID(b))
	}
	return set, nil
}

func flagsToStrings(flags []imap.Flag) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, string(f))
	}
	return out
}

func summaryFromBuffer(buf *imapclient.FetchMessageBuffer) MessageSummary {
	s := MessageSummary{
		UID:    uint32(buf.UID),
		SeqNum: buf.SeqNum,
		Flags:  flagsToStrings(buf.Flags),
		Size:   buf.RFC822Size,
	}
	if !buf.InternalDate.IsZero() {
		s.InternalDate = buf.InternalDate
	}
	if buf.Envelope != nil {
		s.Subject = buf.Envelope.Subject
		s.From = addrList(buf.Envelope.From)
		s.To = addrList(buf.Envelope.To)
		s.Cc = addrList(buf.Envelope.Cc)
		s.MessageID = buf.Envelope.MessageID
	}
	return s
}

// ListMailboxes lists mailboxes. pattern is an IMAP LIST pattern (e.g. "*").
// If subscribedOnly, only subscribed mailboxes are returned. If withStatus,
// STATUS data is requested alongside (LIST-STATUS extension).
func (s *Session) ListMailboxes(pattern string, subscribedOnly, withStatus bool) ([]MailboxInfo, error) {
	if pattern == "" {
		pattern = "*"
	}
	opts := &imap.ListOptions{}
	if subscribedOnly {
		opts.SelectSubscribed = true
	}
	if withStatus {
		opts.ReturnStatus = &imap.StatusOptions{
			NumMessages: true, NumUnseen: true, UIDNext: true, UIDValidity: true,
		}
	}
	var out []MailboxInfo
	err := s.withClient(func(c *imapclient.Client) error {
		data, err := c.List("", pattern, opts).Collect()
		if err != nil {
			return err
		}
		for _, d := range data {
			info := MailboxInfo{
				Name:       d.Mailbox,
				Attributes: mailboxAttrsToStrings(d.Attrs),
			}
			if d.Delim != 0 {
				info.Delimiter = string(d.Delim)
			}
			for _, a := range d.Attrs {
				if a == imap.MailboxAttrSubscribed {
					info.Subscribed = true
				}
			}
			if d.Status != nil {
				info.NumMessages = d.Status.NumMessages
				info.NumUnseen = d.Status.NumUnseen
				info.UIDNext = uint32(d.Status.UIDNext)
				info.UIDValidity = d.Status.UIDValidity
			}
			out = append(out, info)
		}
		return nil
	})
	return out, err
}

func mailboxAttrsToStrings(attrs []imap.MailboxAttr) []string {
	out := make([]string, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, string(a))
	}
	return out
}

// CreateMailbox creates a mailbox.
func (s *Session) CreateMailbox(name string) error {
	return s.withClient(func(c *imapclient.Client) error {
		return c.Create(name, nil).Wait()
	})
}

// DeleteMailbox deletes a mailbox.
func (s *Session) DeleteMailbox(name string) error {
	return s.withClient(func(c *imapclient.Client) error {
		return c.Delete(name).Wait()
	})
}

// RenameMailbox renames a mailbox.
func (s *Session) RenameMailbox(oldName, newName string) error {
	return s.withClient(func(c *imapclient.Client) error {
		return c.Rename(oldName, newName, nil).Wait()
	})
}

// Subscribe marks a mailbox as subscribed.
func (s *Session) Subscribe(name string) error {
	return s.withClient(func(c *imapclient.Client) error {
		return c.Subscribe(name).Wait()
	})
}

// Unsubscribe removes the subscribed mark.
func (s *Session) Unsubscribe(name string) error {
	return s.withClient(func(c *imapclient.Client) error {
		return c.Unsubscribe(name).Wait()
	})
}

// MailboxStatus returns STATUS data for a mailbox without selecting it.
func (s *Session) MailboxStatus(mailbox string) (*MailboxStatus, error) {
	opts := &imap.StatusOptions{
		NumMessages: true, NumUnseen: true, NumDeleted: true,
		UIDNext: true, UIDValidity: true, Size: true,
	}
	var st *MailboxStatus
	err := s.withClient(func(c *imapclient.Client) error {
		data, err := c.Status(mailbox, opts).Wait()
		if err != nil {
			return err
		}
		st = &MailboxStatus{
			Mailbox:     data.Mailbox,
			NumMessages: data.NumMessages,
			NumUnseen:   data.NumUnseen,
			NumDeleted:  data.NumDeleted,
			UIDNext:     uint32(data.UIDNext),
			UIDValidity: data.UIDValidity,
			Size:        data.Size,
		}
		return nil
	})
	return st, err
}

// selectMailbox selects a mailbox read-write.
func selectMailbox(c *imapclient.Client, mailbox string) (*imap.SelectData, error) {
	return c.Select(mailbox, &imap.SelectOptions{ReadOnly: false}).Wait()
}

// SearchMessages runs a UID SEARCH in the given mailbox and returns matching UIDs.
func (s *Session) SearchMessages(mailbox string, q *SearchQuery) ([]uint32, error) {
	criteria, err := q.criteria()
	if err != nil {
		return nil, err
	}
	var uids []uint32
	err = s.withClient(func(c *imapclient.Client) error {
		if _, err := selectMailbox(c, mailbox); err != nil {
			return fmt.Errorf("selecting %s: %w", mailbox, err)
		}
		data, err := c.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return err
		}
		if data.All == nil {
			return nil
		}
		nums, ok := data.All.(imap.UIDSet).Nums()
		if !ok {
			// Fall back to parsing the string form, e.g. "1 2 5:9".
			for _, u := range parseNumSetString(data.All.String()) {
				uids = append(uids, uint32(u))
			}
		} else {
			for _, n := range nums {
				uids = append(uids, uint32(n))
			}
		}
		sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
		return nil
	})
	return uids, err
}

// parseNumSetString parses an IMAP number-set string like "1 2 5:9" into UIDs.
func parseNumSetString(s string) []imap.UID {
	var out []imap.UID
	for _, part := range strings.Fields(s) {
		start, stop, hasRange := strings.Cut(part, ":")
		var a, b uint32
		fmt.Sscanf(start, "%d", &a)
		if hasRange {
			if stop == "*" {
				b = ^uint32(0)
			} else {
				fmt.Sscanf(stop, "%d", &b)
			}
		} else {
			b = a
		}
		for u := a; u <= b && u != 0; u++ {
			out = append(out, imap.UID(u))
			if u == b {
				break
			}
		}
	}
	return out
}

// ListMessages returns message summaries for a UID range (or the last count messages).
// If uids is empty, the last `count` messages (by sequence) are listed.
func (s *Session) ListMessages(mailbox string, uids []uint32, ranges []string, count int) ([]MessageSummary, error) {
	if count <= 0 {
		count = 20
	}
	var set imap.UIDSet
	var err error
	if len(uids) > 0 || len(ranges) > 0 {
		set, err = uidSet(uids, ranges)
		if err != nil {
			return nil, err
		}
	}
	var out []MessageSummary
	err = s.withClient(func(c *imapclient.Client) error {
		data, err := selectMailbox(c, mailbox)
		if err != nil {
			return fmt.Errorf("selecting %s: %w", mailbox, err)
		}
		var numSet imap.NumSet
		if set != nil {
			numSet = set
		} else {
			// List the last `count` messages by sequence number.
			n := data.NumMessages
			start := uint32(1)
			if n > uint32(count) {
				start = n - uint32(count) + 1
			}
			seqSet := imap.SeqSetNum()
			seqSet.AddRange(start, n)
			numSet = seqSet
		}
		cmd := c.Fetch(numSet, &imap.FetchOptions{
			UID:          true,
			Envelope:     true,
			Flags:        true,
			InternalDate: true,
			RFC822Size:   true,
		})
		bufs, err := cmd.Collect()
		if err != nil {
			cmd.Close()
			return err
		}
		for _, buf := range bufs {
			out = append(out, summaryFromBuffer(buf))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
		return nil
	})
	return out, err
}

// GetMessage fetches a full message by UID: envelope, flags, headers, text and
// HTML bodies, and an attachment list (without attachment contents).
func (s *Session) GetMessage(mailbox string, uid uint32) (*Message, error) {
	var msg *Message
	err := s.withClient(func(c *imapclient.Client) error {
		if _, err := selectMailbox(c, mailbox); err != nil {
			return fmt.Errorf("selecting %s: %w", mailbox, err)
		}
		set := imap.UIDSetNum(imap.UID(uid))
		headerSection := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader}
		textSection := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierText}
		cmd := c.Fetch(set, &imap.FetchOptions{
			UID:           true,
			Envelope:      true,
			Flags:         true,
			InternalDate:  true,
			RFC822Size:    true,
			BodyStructure: &imap.FetchItemBodyStructure{},
			BodySection:   []*imap.FetchItemBodySection{headerSection, textSection, {}},
		})
		bufs, err := cmd.Collect()
		if err != nil {
			cmd.Close()
			return err
		}
		if len(bufs) == 0 {
			return fmt.Errorf("message with UID %d not found in %s", uid, mailbox)
		}
		buf := bufs[0]
		msg = &Message{
			UID:          uint32(buf.UID),
			SeqNum:       buf.SeqNum,
			Flags:        flagsToStrings(buf.Flags),
			InternalDate: buf.InternalDate,
			Size:         buf.RFC822Size,
		}
		if buf.Envelope != nil {
			e := buf.Envelope
			msg.Subject = e.Subject
			msg.From = addrList(e.From)
			msg.To = addrList(e.To)
			msg.Cc = addrList(e.Cc)
			msg.Bcc = addrList(e.Bcc)
			msg.ReplyTo = addrList(e.ReplyTo)
			msg.MessageID = e.MessageID
			msg.InReplyTo = e.InReplyTo
		}
		rawHeader := buf.FindBodySection(headerSection)
		rawText := buf.FindBodySection(textSection)
		rawFull := buf.FindBodySection(&imap.FetchItemBodySection{})
		if len(rawHeader) > 0 {
			msg.Headers = parseHeaders(rawHeader)
		}
		full := rawFull
		if len(full) == 0 {
			full = append(append([]byte{}, rawHeader...), rawText...)
		}
		msg.TextBody, msg.HTMLBody = extractBodies(full)
		if buf.BodyStructure != nil {
			msg.Attachments = walkBodyStructure(buf.BodyStructure)
		}
		return nil
	})
	return msg, err
}

// GetAttachment fetches the raw bytes of one MIME part of a message.
// partID is dot-separated part numbers, e.g. "2" or "1.2".
func (s *Session) GetAttachment(mailbox string, uid uint32, partID string) ([]byte, error) {
	var part []int
	for _, p := range strings.Split(partID, ".") {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil || n < 1 {
			return nil, fmt.Errorf("invalid part ID %q", partID)
		}
		part = append(part, n)
	}
	var data []byte
	err := s.withClient(func(c *imapclient.Client) error {
		if _, err := selectMailbox(c, mailbox); err != nil {
			return fmt.Errorf("selecting %s: %w", mailbox, err)
		}
		set := imap.UIDSetNum(imap.UID(uid))
		section := &imap.FetchItemBodySection{Part: part}
		cmd := c.Fetch(set, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}})
		bufs, err := cmd.Collect()
		if err != nil {
			cmd.Close()
			return err
		}
		if len(bufs) == 0 {
			return fmt.Errorf("message with UID %d not found in %s", uid, mailbox)
		}
		data = bufs[0].FindBodySection(section)
		if data == nil {
			return fmt.Errorf("part %q not found in message UID %d", partID, uid)
		}
		return nil
	})
	return data, err
}

// SetFlags adds, removes, or replaces flags on messages.
// op is "add", "remove", or "set".
func (s *Session) SetFlags(mailbox string, uids []uint32, ranges []string, op string, flags []string) error {
	set, err := uidSet(uids, ranges)
	if err != nil {
		return err
	}
	imapFlags := make([]imap.Flag, 0, len(flags))
	for _, f := range flags {
		if !strings.HasPrefix(f, "\\") {
			f = "\\" + f
		}
		imapFlags = append(imapFlags, imap.Flag(f))
	}
	var storeOp imap.StoreFlagsOp
	switch op {
	case "add":
		storeOp = imap.StoreFlagsAdd
	case "remove":
		storeOp = imap.StoreFlagsDel
	case "set", "":
		storeOp = imap.StoreFlagsSet
	default:
		return fmt.Errorf("invalid flag op %q (want add|remove|set)", op)
	}
	return s.withClient(func(c *imapclient.Client) error {
		if _, err := selectMailbox(c, mailbox); err != nil {
			return fmt.Errorf("selecting %s: %w", mailbox, err)
		}
		cmd := c.Store(set, &imap.StoreFlags{Op: storeOp, Flags: imapFlags}, nil)
		if _, err := cmd.Collect(); err != nil {
			cmd.Close()
			return err
		}
		return nil
	})
}

// MoveMessages moves messages to another mailbox (UID MOVE if supported, else UID COPY + \Deleted + EXPUNGE).
func (s *Session) MoveMessages(mailbox, dest string, uids []uint32, ranges []string) error {
	set, err := uidSet(uids, ranges)
	if err != nil {
		return err
	}
	return s.withClient(func(c *imapclient.Client) error {
		if _, err := selectMailbox(c, mailbox); err != nil {
			return fmt.Errorf("selecting %s: %w", mailbox, err)
		}
		if c.Caps().Has(imap.CapMove) {
			_, err := c.Move(set, dest).Wait()
			return err
		}
		if _, err := c.Copy(set, dest).Wait(); err != nil {
			return err
		}
		store := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil)
		if _, err := store.Collect(); err != nil {
			store.Close()
			return err
		}
		_, err := c.Expunge().Collect()
		return err
	})
}

// CopyMessages copies messages to another mailbox.
func (s *Session) CopyMessages(mailbox, dest string, uids []uint32, ranges []string) error {
	set, err := uidSet(uids, ranges)
	if err != nil {
		return err
	}
	return s.withClient(func(c *imapclient.Client) error {
		if _, err := selectMailbox(c, mailbox); err != nil {
			return fmt.Errorf("selecting %s: %w", mailbox, err)
		}
		_, err := c.Copy(set, dest).Wait()
		return err
	})
}

// DeleteMessages marks messages \Deleted; if permanent, also expunges them.
func (s *Session) DeleteMessages(mailbox string, uids []uint32, ranges []string, permanent bool) error {
	set, err := uidSet(uids, ranges)
	if err != nil {
		return err
	}
	return s.withClient(func(c *imapclient.Client) error {
		if _, err := selectMailbox(c, mailbox); err != nil {
			return fmt.Errorf("selecting %s: %w", mailbox, err)
		}
		store := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil)
		if _, err := store.Collect(); err != nil {
			store.Close()
			return err
		}
		if !permanent {
			return nil
		}
		_, err := c.UIDExpunge(set).Collect()
		return err
	})
}

// AppendMessage appends a raw RFC 5322 message to a mailbox.
func (s *Session) AppendMessage(mailbox string, raw []byte, flags []string) error {
	imapFlags := make([]imap.Flag, 0, len(flags))
	for _, f := range flags {
		if !strings.HasPrefix(f, "\\") {
			f = "\\" + f
		}
		imapFlags = append(imapFlags, imap.Flag(f))
	}
	return s.withClient(func(c *imapclient.Client) error {
		cmd := c.Append(mailbox, int64(len(raw)), &imap.AppendOptions{Flags: imapFlags})
		if _, err := cmd.Write(raw); err != nil {
			cmd.Close()
			return err
		}
		if err := cmd.Close(); err != nil {
			return err
		}
		_, err := cmd.Wait()
		return err
	})
}

// QuotaInfo is a simplified quota root entry.
type QuotaInfo struct {
	Root     string `json:"root"`
	Resource string `json:"resource"`
	Used     int64  `json:"used"`
	Limit    int64  `json:"limit"`
}

// GetQuota returns quota information for all quota roots of a mailbox.
func (s *Session) GetQuota(mailbox string) ([]QuotaInfo, error) {
	var out []QuotaInfo
	err := s.withClient(func(c *imapclient.Client) error {
		roots, err := c.GetQuotaRoot(mailbox).Wait()
		if err != nil {
			return err
		}
		for _, root := range roots {
			data, err := c.GetQuota(root.Root).Wait()
			if err != nil {
				return err
			}
			for resType, res := range data.Resources {
				out = append(out, QuotaInfo{
					Root:     root.Root,
					Resource: string(resType),
					Used:     res.Usage,
					Limit:    res.Limit,
				})
			}
		}
		return nil
	})
	return out, err
}
