package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/mail"
)

func registerResources(s *mcp.Server, sess *mail.Session) {
	// Static resource: the mailbox list.
	s.AddResource(&mcp.Resource{
		URI:         "mail://mailboxes",
		Name:        "Mailboxes",
		Description: "JSON list of all mailboxes (folders) in this account",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		boxes, err := sess.ListMailboxes("*", false, true)
		if err != nil {
			return nil, err
		}
		data, err := json.MarshalIndent(boxes, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(data),
			}},
		}, nil
	})

	// Template: mail://mailbox/{mailbox} — mailbox status.
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "mail://mailbox/{mailbox}",
		Name:        "Mailbox status",
		Description: "STATUS of one mailbox as JSON (message count, unseen, UIDNEXT, UIDVALIDITY)",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		mailbox, err := mailboxFromURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		st, err := sess.MailboxStatus(mailbox)
		if err != nil {
			return nil, err
		}
		data, _ := json.MarshalIndent(st, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(data),
			}},
		}, nil
	})

	// Template: mail://message/{mailbox}/{uid} — full message as JSON.
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "mail://message/{mailbox}/{uid}",
		Name:        "Message",
		Description: "A full message (headers, text/HTML bodies, attachment list) as JSON. Mailbox names containing '/' are URL-encoded.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		mailbox, uid, err := messageFromURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		msg, err := sess.GetMessage(mailbox, uid)
		if err != nil {
			return nil, err
		}
		data, _ := json.MarshalIndent(msg, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(data),
			}},
		}, nil
	})

	// Template: mail://attachment/{mailbox}/{uid}/{part} — raw attachment, base64 in JSON.
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "mail://attachment/{mailbox}/{uid}/{part}",
		Name:        "Attachment",
		Description: "One MIME part (attachment) of a message, base64-encoded, as JSON",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		mailbox, uid, part, err := attachmentFromURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		data, err := sess.GetAttachment(mailbox, uid, part)
		if err != nil {
			return nil, err
		}
		payload, _ := json.Marshal(map[string]any{
			"part_id":        part,
			"size":           len(data),
			"content_base64": b64Encode(data),
		})
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(payload),
			}},
		}, nil
	})
}

// mailboxFromURI extracts the mailbox from mail://mailbox/{mailbox}.
// The mailbox may itself contain '/', which clients must percent-encode.
func mailboxFromURI(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, "mail://mailbox/")
	if !ok || rest == "" {
		return "", fmt.Errorf("invalid mailbox resource URI %q (want mail://mailbox/{mailbox})", uri)
	}
	return rest, nil
}

func messageFromURI(uri string) (string, uint32, error) {
	rest, ok := strings.CutPrefix(uri, "mail://message/")
	if !ok {
		return "", 0, fmt.Errorf("invalid message resource URI %q (want mail://message/{mailbox}/{uid})", uri)
	}
	mailbox, uidStr, ok := strings.Cut(rest, "/")
	if !ok || mailbox == "" || uidStr == "" {
		return "", 0, fmt.Errorf("invalid message resource URI %q", uri)
	}
	uid64, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid UID in resource URI %q", uri)
	}
	return mailbox, uint32(uid64), nil
}

func attachmentFromURI(uri string) (string, uint32, string, error) {
	rest, ok := strings.CutPrefix(uri, "mail://attachment/")
	if !ok {
		return "", 0, "", fmt.Errorf("invalid attachment resource URI %q", uri)
	}
	mailbox, rest2, ok := strings.Cut(rest, "/")
	if !ok {
		return "", 0, "", fmt.Errorf("invalid attachment resource URI %q", uri)
	}
	uidStr, part, ok := strings.Cut(rest2, "/")
	if !ok || part == "" {
		return "", 0, "", fmt.Errorf("invalid attachment resource URI %q", uri)
	}
	uid64, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		return "", 0, "", fmt.Errorf("invalid UID in resource URI %q", uri)
	}
	return mailbox, uint32(uid64), part, nil
}
