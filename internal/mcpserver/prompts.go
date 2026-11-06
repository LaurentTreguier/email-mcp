package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/mail"
)

func registerPrompts(s *mcp.Server, sess *mail.Session) {
	arg := func(name, desc string, required bool) *mcp.PromptArgument {
		return &mcp.PromptArgument{Name: name, Description: desc, Required: required}
	}

	s.AddPrompt(&mcp.Prompt{
		Name:        "triage_inbox",
		Description: "Review the newest messages in a mailbox and propose actions (reply, archive, flag, delete).",
		Arguments:   []*mcp.PromptArgument{arg("mailbox", "Mailbox to triage, e.g. INBOX", false), arg("count", "How many recent messages to review (default 20)", false)},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		mailbox := req.Params.Arguments["mailbox"]
		if mailbox == "" {
			mailbox = "INBOX"
		}
		count := req.Params.Arguments["count"]
		if count == "" {
			count = "20"
		}
		return &mcp.GetPromptResult{
			Description: fmt.Sprintf("Triage the %s mailbox", mailbox),
			Messages: []*mcp.PromptMessage{{
				Role: "user",
				Content: &mcp.TextContent{Text: fmt.Sprintf(`Triage my %s mailbox:
1. Use list_messages with mailbox=%q and count=%s to fetch the newest messages.
2. For each message, briefly summarize sender, subject, date, and content (use get_message for anything unclear).
3. Propose an action for each message: reply, archive (move to Archive), flag, mark read/unread, or delete.
4. Do NOT send, move, or delete anything without asking me first — present the plan and wait for confirmation.`, mailbox, mailbox, count)},
			}},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "summarize_thread",
		Description: "Summarize an email thread starting from a message UID.",
		Arguments:   []*mcp.PromptArgument{arg("mailbox", "Mailbox containing the message", true), arg("uid", "UID of a message in the thread", true)},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		mailbox := req.Params.Arguments["mailbox"]
		uid := req.Params.Arguments["uid"]
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{{
				Role: "user",
				Content: &mcp.TextContent{Text: fmt.Sprintf(`Summarize the email thread containing message UID %s in mailbox %q:
1. Use get_message on mailbox=%q, uid=%s to read the message, including its In-Reply-To/References and Message-ID.
2. Use search_messages to find related messages (search by subject and by the Message-ID/References values in the same mailbox).
3. Read the other messages with get_message and produce a chronological summary of the whole thread: participants, key points, decisions, and open questions.`, uid, mailbox, mailbox, uid)},
			}},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "draft_reply",
		Description: "Draft a reply to a message, ready for review before sending.",
		Arguments:   []*mcp.PromptArgument{arg("mailbox", "Mailbox containing the message", true), arg("uid", "UID of the message to reply to", true), arg("tone", "Desired tone, e.g. formal, friendly, concise", false)},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		mailbox := req.Params.Arguments["mailbox"]
		uid := req.Params.Arguments["uid"]
		tone := req.Params.Arguments["tone"]
		if tone == "" {
			tone = "professional and concise"
		}
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{{
				Role: "user",
				Content: &mcp.TextContent{Text: fmt.Sprintf(`Draft a reply to message UID %s in mailbox %q:
1. Use get_message on mailbox=%q, uid=%s and read it fully (headers, text body, attachments list).
2. Compose a %s reply addressed to the original sender (use Reply-To if present, else From), keeping the original recipients in Cc if appropriate.
3. Set In-Reply-To and References to the original Message-ID so the reply threads correctly.
4. Show me the draft (To/Cc/Subject/body) and DO NOT send it — wait for my approval, then use send_email with in_reply_to set.`, uid, mailbox, mailbox, uid, tone)},
			}},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "find_unanswered",
		Description: "Find messages you received that are not flagged \\Answered and may need a reply.",
		Arguments:   []*mcp.PromptArgument{arg("mailbox", "Mailbox to scan, e.g. INBOX", false), arg("days", "Only consider messages from the last N days", false)},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		mailbox := req.Params.Arguments["mailbox"]
		if mailbox == "" {
			mailbox = "INBOX"
		}
		days := req.Params.Arguments["days"]
		since := ""
		if days != "" {
			since = fmt.Sprintf(" (from the last %s days)", days)
		}
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{{
				Role: "user",
				Content: &mcp.TextContent{Text: fmt.Sprintf(`Find messages in my %s mailbox%s that likely need a reply:
1. Use search_messages with mailbox=%q, query.answered=false, query.seen=true%s.
2. For the most promising ones, use get_message to check whether they actually ask a question or request something.
3. List them with sender, subject, date, UID, and a one-line reason each needs a reply. Do not reply automatically.`, mailbox, since, mailbox, daysSinceArg(days))},
			}},
		}, nil
	})
}

func daysSinceArg(days string) string {
	n := 0
	if _, err := fmt.Sscanf(days, "%d", &n); err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf(", query.since=%q", time.Now().AddDate(0, 0, -n).Format("2006-01-02"))
}
