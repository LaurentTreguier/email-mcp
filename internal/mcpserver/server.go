// Package mcpserver wires a mail account into an MCP server.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/config"
	"email-mcp/internal/mail"
)

// New builds an MCP server exposing one account's IMAP/SMTP mailbox.
func New(acc *config.Account) *mcp.Server {
	sess := mail.NewSession(acc)
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "email-mcp/" + acc.Domain(),
		Version: "v0.1.0",
	}, &mcp.ServerOptions{
		Instructions: fmt.Sprintf("Mail account %s. Use the tools to list mailboxes, search and read messages, manage flags and folders, and send email.", acc.Email),
		Capabilities: &mcp.ServerCapabilities{},
	})
	track(s, sess)
	registerTools(s, sess)
	registerResources(s, sess)
	registerPrompts(s, sess)
	// Ask the user for credentials (elicitation) on the first tool call that
	// needs a login, if no password is configured.
	s.AddReceivingMiddleware(credentialMiddleware(sess))
	return s
}

// credentialMiddleware intercepts tools/call: if the account has no
// password yet, elicit one from the user before running the tool.
func credentialMiddleware(sess *mail.Session) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if ctReq, ok := req.(*mcp.CallToolRequest); ok {
					if _, err := ensureCredentials(ctx, ctReq, sess); err != nil {
						return &mcp.CallToolResult{
							IsError: true,
							Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
						}, nil
					}
				}
			}
			return next(ctx, method, req)
		}
	}
}

// Session returns the mail session backing a server (for lifecycle management).
func SessionOf(s *mcp.Server) *mail.Session { return sessions[s] }

var sessions = map[*mcp.Server]*mail.Session{}

func track(s *mcp.Server, sess *mail.Session) { sessions[s] = sess }
