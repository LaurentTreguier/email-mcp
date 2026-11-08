// Package mcpserver wires a mail account into an MCP server.
package mcpserver

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/config"
	"email-mcp/internal/mail"
)

// New builds an MCP server exposing one account's IMAP/SMTP mailbox.
func New(acc *config.Account) *mcp.Server {
	sess := mail.NewSession(acc)
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "email-mcp/" + acc.Email,
		Version: "v0.4.2",
	}, &mcp.ServerOptions{
		Instructions: fmt.Sprintf("Mail account %s. Use the tools to list mailboxes, search and read messages, manage flags and folders, and send email.", acc.Email),
		Capabilities: &mcp.ServerCapabilities{},
	})
	registerTools(s, sess)
	registerResources(s, sess)
	registerPrompts(s, sess)
	// The account (including its credential) arrives fully formed inside the
	// OAuth access token; no runtime elicitation is needed.
	return s
}
