package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/mail"
)

func registerTools(s *mcp.Server, sess *mail.Session) {
	type accountInput struct{}
	type accountOutput struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name,omitempty"`
		Domain      string `json:"domain"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "account_info",
		Description: "Show the mail account this server endpoint is bound to (selected by the URL path domain).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in accountInput) (*mcp.CallToolResult, accountOutput, error) {
		acc := sess.Account()
		return nil, accountOutput{Email: acc.Email, DisplayName: acc.DisplayName, Domain: acc.Domain()}, nil
	})

	type listMailboxesInput struct {
		Pattern        string `json:"pattern,omitempty" jsonschema:"IMAP LIST pattern, default '*'"`
		SubscribedOnly bool   `json:"subscribed_only,omitempty" jsonschema:"Only return subscribed mailboxes (LSUB)"`
		WithStatus     bool   `json:"with_status,omitempty" jsonschema:"Include STATUS data (message/unseen counts, UIDNEXT, UIDVALIDITY)"`
	}
	type listMailboxesOutput struct {
		Mailboxes []mail.MailboxInfo `json:"mailboxes"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_mailboxes",
		Description: "List IMAP mailboxes (folders) of the account, optionally with status counts.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listMailboxesInput) (*mcp.CallToolResult, listMailboxesOutput, error) {
		boxes, err := sess.ListMailboxes(in.Pattern, in.SubscribedOnly, in.WithStatus)
		if err != nil {
			return nil, listMailboxesOutput{}, err
		}
		return nil, listMailboxesOutput{Mailboxes: boxes}, nil
	})

	type mailboxOpInput struct {
		Mailbox string `json:"mailbox" jsonschema:"Mailbox name, e.g. INBOX"`
	}
	type emptyOutput struct {
		OK bool `json:"ok"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_mailbox",
		Description: "Create a new mailbox (folder).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mailboxOpInput) (*mcp.CallToolResult, emptyOutput, error) {
		return nil, emptyOutput{}, sess.CreateMailbox(in.Mailbox)
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_mailbox",
		Description: "Delete a mailbox and all its messages.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mailboxOpInput) (*mcp.CallToolResult, emptyOutput, error) {
		return nil, emptyOutput{}, sess.DeleteMailbox(in.Mailbox)
	})

	type renameInput struct {
		Mailbox string `json:"mailbox" jsonschema:"Existing mailbox name"`
		NewName string `json:"new_name" jsonschema:"New mailbox name"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "rename_mailbox",
		Description: "Rename a mailbox.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in renameInput) (*mcp.CallToolResult, emptyOutput, error) {
		return nil, emptyOutput{}, sess.RenameMailbox(in.Mailbox, in.NewName)
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "subscribe_mailbox",
		Description: "Mark a mailbox as subscribed (shows up in LSUB).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mailboxOpInput) (*mcp.CallToolResult, emptyOutput, error) {
		return nil, emptyOutput{}, sess.Subscribe(in.Mailbox)
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "unsubscribe_mailbox",
		Description: "Remove the subscribed mark from a mailbox.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mailboxOpInput) (*mcp.CallToolResult, emptyOutput, error) {
		return nil, emptyOutput{}, sess.Unsubscribe(in.Mailbox)
	})

	type statusOutput struct {
		Status *mail.MailboxStatus `json:"status"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "mailbox_status",
		Description: "Get STATUS of a mailbox without selecting it: message count, unseen count, UIDNEXT, UIDVALIDITY, size.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mailboxOpInput) (*mcp.CallToolResult, statusOutput, error) {
		st, err := sess.MailboxStatus(in.Mailbox)
		if err != nil {
			return nil, statusOutput{}, err
		}
		return nil, statusOutput{Status: st}, nil
	})

	type searchInput struct {
		Mailbox string           `json:"mailbox" jsonschema:"Mailbox to search, e.g. INBOX"`
		Query   mail.SearchQuery `json:"query" jsonschema:"Search criteria; all fields optional and ANDed together"`
	}
	type searchOutput struct {
		UIDs []uint32 `json:"uids"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_messages",
		Description: "UID SEARCH messages in a mailbox. Returns matching message UIDs, sorted ascending.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		uids, err := sess.SearchMessages(in.Mailbox, &in.Query)
		if err != nil {
			return nil, searchOutput{}, err
		}
		return nil, searchOutput{UIDs: uids}, nil
	})

	type listMessagesInput struct {
		Mailbox string   `json:"mailbox" jsonschema:"Mailbox, e.g. INBOX"`
		UIDs    []uint32 `json:"uids,omitempty" jsonschema:"Specific message UIDs to fetch summaries for"`
		Ranges  []string `json:"ranges,omitempty" jsonschema:"UID ranges like '10:20' (stop may be '*')"`
		Count   int      `json:"count,omitempty" jsonschema:"If no uids/ranges given, list the last N messages (default 20)"`
	}
	type listMessagesOutput struct {
		Messages []mail.MessageSummary `json:"messages"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_messages",
		Description: "List message summaries (envelope, flags, size) in a mailbox. By default the most recent messages.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listMessagesInput) (*mcp.CallToolResult, listMessagesOutput, error) {
		msgs, err := sess.ListMessages(in.Mailbox, in.UIDs, in.Ranges, in.Count)
		if err != nil {
			return nil, listMessagesOutput{}, err
		}
		return nil, listMessagesOutput{Messages: msgs}, nil
	})

	type getMessageInput struct {
		Mailbox string `json:"mailbox" jsonschema:"Mailbox, e.g. INBOX"`
		UID     uint32 `json:"uid" jsonschema:"Message UID (from search_messages or list_messages)"`
	}
	type getMessageOutput struct {
		Message *mail.Message `json:"message"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_message",
		Description: "Fetch a full message by UID: headers, text and HTML bodies, flags, and the list of attachments (not their contents).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getMessageInput) (*mcp.CallToolResult, getMessageOutput, error) {
		msg, err := sess.GetMessage(in.Mailbox, in.UID)
		if err != nil {
			return nil, getMessageOutput{}, err
		}
		return nil, getMessageOutput{Message: msg}, nil
	})

	type getAttachmentInput struct {
		Mailbox string `json:"mailbox" jsonschema:"Mailbox, e.g. INBOX"`
		UID     uint32 `json:"uid" jsonschema:"Message UID"`
		PartID  string `json:"part_id" jsonschema:"MIME part id from the message's attachments list, e.g. '2' or '1.2'"`
	}
	type getAttachmentOutput struct {
		PartID        string `json:"part_id"`
		ContentBase64 string `json:"content_base64"`
		Size          int    `json:"size"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_attachment",
		Description: "Download one attachment (MIME part) of a message, base64-encoded.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getAttachmentInput) (*mcp.CallToolResult, getAttachmentOutput, error) {
		data, err := sess.GetAttachment(in.Mailbox, in.UID, in.PartID)
		if err != nil {
			return nil, getAttachmentOutput{}, err
		}
		return nil, getAttachmentOutput{
			PartID:        in.PartID,
			ContentBase64: b64Encode(data),
			Size:          len(data),
		}, nil
	})

	type flagsInput struct {
		Mailbox string   `json:"mailbox" jsonschema:"Mailbox, e.g. INBOX"`
		UIDs    []uint32 `json:"uids,omitempty" jsonschema:"Message UIDs"`
		Ranges  []string `json:"ranges,omitempty" jsonschema:"UID ranges like '10:20'"`
		Op      string   `json:"op,omitempty" jsonschema:"add, remove, or set (default)" enum:"add,remove,set"`
		Flags   []string `json:"flags" jsonschema:"IMAP flags, e.g. [\"\\\\Seen\", \"\\\\Flagged\"]"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_flags",
		Description: "Add, remove, or set flags (\\Seen, \\Answered, \\Flagged, \\Deleted, \\Draft, or custom keywords) on messages.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in flagsInput) (*mcp.CallToolResult, emptyOutput, error) {
		if err := sess.SetFlags(in.Mailbox, in.UIDs, in.Ranges, in.Op, in.Flags); err != nil {
			return nil, emptyOutput{}, err
		}
		return nil, emptyOutput{OK: true}, nil
	})

	type moveInput struct {
		Mailbox string   `json:"mailbox" jsonschema:"Source mailbox"`
		Dest    string   `json:"dest" jsonschema:"Destination mailbox"`
		UIDs    []uint32 `json:"uids,omitempty"`
		Ranges  []string `json:"ranges,omitempty" jsonschema:"UID ranges like '10:20'"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "move_messages",
		Description: "Move messages to another mailbox (uses UID MOVE when supported).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in moveInput) (*mcp.CallToolResult, emptyOutput, error) {
		if err := sess.MoveMessages(in.Mailbox, in.Dest, in.UIDs, in.Ranges); err != nil {
			return nil, emptyOutput{}, err
		}
		return nil, emptyOutput{OK: true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "copy_messages",
		Description: "Copy messages to another mailbox, leaving the originals in place.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in moveInput) (*mcp.CallToolResult, emptyOutput, error) {
		if err := sess.CopyMessages(in.Mailbox, in.Dest, in.UIDs, in.Ranges); err != nil {
			return nil, emptyOutput{}, err
		}
		return nil, emptyOutput{OK: true}, nil
	})

	type deleteInput struct {
		Mailbox   string   `json:"mailbox" jsonschema:"Mailbox"`
		UIDs      []uint32 `json:"uids,omitempty"`
		Ranges    []string `json:"ranges,omitempty" jsonschema:"UID ranges like '10:20'"`
		Permanent bool     `json:"permanent,omitempty" jsonschema:"If true, expunge immediately (UID EXPUNGE); otherwise just mark \\Deleted"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_messages",
		Description: "Delete messages: marks them \\Deleted and optionally expunges them permanently.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deleteInput) (*mcp.CallToolResult, emptyOutput, error) {
		if err := sess.DeleteMessages(in.Mailbox, in.UIDs, in.Ranges, in.Permanent); err != nil {
			return nil, emptyOutput{}, err
		}
		return nil, emptyOutput{OK: true}, nil
	})

	type sendInput struct {
		To          []string          `json:"to" jsonschema:"Recipient addresses"`
		Cc          []string          `json:"cc,omitempty"`
		Bcc         []string          `json:"bcc,omitempty"`
		Subject     string            `json:"subject"`
		TextBody    string            `json:"text_body,omitempty" jsonschema:"Plain-text body"`
		HTMLBody    string            `json:"html_body,omitempty" jsonschema:"HTML body (sent alongside text)"`
		Attachments []mail.Attachment `json:"attachments,omitempty"`
		InReplyTo   string            `json:"in_reply_to,omitempty" jsonschema:"Message-ID of the message being replied to (without angle brackets), for threading"`
		References  []string          `json:"references,omitempty" jsonschema:"Additional Message-IDs for the References header"`
		SaveToSent  *bool             `json:"save_to_sent,omitempty" jsonschema:"Append a copy to the Sent mailbox (default true)"`
	}
	type sendOutput struct {
		Sent bool   `json:"sent"`
		Note string `json:"note,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "send_email",
		Description: "Compose and send an email via SMTP submission, then save a copy to the Sent mailbox. Supports attachments (base64) and reply threading.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in sendInput) (*mcp.CallToolResult, sendOutput, error) {
		acc := sess.Account()
		raw, err := mail.Compose(acc, &mail.OutgoingMessage{
			To:          in.To,
			Cc:          in.Cc,
			Bcc:         in.Bcc,
			Subject:     in.Subject,
			TextBody:    in.TextBody,
			HTMLBody:    in.HTMLBody,
			Attachments: in.Attachments,
			InReplyTo:   in.InReplyTo,
			References:  in.References,
		})
		if err != nil {
			return nil, sendOutput{}, err
		}
		recipients := append(append(append([]string{}, in.To...), in.Cc...), in.Bcc...)
		saveToSent := in.SaveToSent == nil || *in.SaveToSent
		if err := sess.Send(raw, acc.Email, recipients, saveToSent); err != nil {
			return nil, sendOutput{}, err
		}
		note := ""
		if saveToSent {
			note = "copy saved to " + acc.SentMailbox
		}
		return nil, sendOutput{Sent: true, Note: note}, nil
	})

	type draftInput struct {
		Mailbox   string   `json:"mailbox,omitempty" jsonschema:"Defaults to the account's Drafts mailbox"`
		To        []string `json:"to,omitempty"`
		Cc        []string `json:"cc,omitempty"`
		Subject   string   `json:"subject,omitempty"`
		TextBody  string   `json:"text_body,omitempty"`
		HTMLBody  string   `json:"html_body,omitempty"`
		InReplyTo string   `json:"in_reply_to,omitempty"`
	}
	type draftOutput struct {
		Saved   bool   `json:"saved"`
		Mailbox string `json:"mailbox"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_draft",
		Description: "Save a message as a draft (IMAP APPEND with \\Draft flag) without sending it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in draftInput) (*mcp.CallToolResult, draftOutput, error) {
		acc := sess.Account()
		mailbox := in.Mailbox
		if mailbox == "" {
			mailbox = acc.DraftsMailbox
		}
		raw, err := mail.Compose(acc, &mail.OutgoingMessage{
			To:        in.To,
			Cc:        in.Cc,
			Subject:   in.Subject,
			TextBody:  in.TextBody,
			HTMLBody:  in.HTMLBody,
			InReplyTo: in.InReplyTo,
		})
		if err != nil {
			return nil, draftOutput{}, err
		}
		if err := sess.AppendMessage(mailbox, raw, []string{"\\Draft"}); err != nil {
			return nil, draftOutput{}, err
		}
		return nil, draftOutput{Saved: true, Mailbox: mailbox}, nil
	})

	type quotaOutput struct {
		Quota []mail.QuotaInfo `json:"quota"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_quota",
		Description: "Get storage quota usage/limits for the account's mailboxes (GETQUOTAROOT + GETQUOTA).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mailboxOpInput) (*mcp.CallToolResult, quotaOutput, error) {
		q, err := sess.GetQuota(in.Mailbox)
		if err != nil {
			return nil, quotaOutput{}, err
		}
		return nil, quotaOutput{Quota: q}, nil
	})
}
