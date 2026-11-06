package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"email-mcp/internal/mail"
)

// ensureCredentials makes sure the session has a password. Resolution order:
//  1. password already configured (config file / env / query-param account)
//  2. elicitation: ask the connected user for it (never via URL)
//
// It returns the password to use, or an error.
func ensureCredentials(ctx context.Context, req *mcp.CallToolRequest, sess *mail.Session) (string, error) {
	acc := sess.Account()
	if pw, err := acc.ResolvePassword(); err == nil && pw != "" {
		return pw, nil
	}
	if req == nil || req.Session == nil {
		return "", fmt.Errorf("no password configured for %s and no client session to ask", acc.Email)
	}
	if req.ClientCapabilities().Elicitation == nil {
		return "", fmt.Errorf("no password configured for %s and the client does not support elicitation", acc.Email)
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"password": map[string]any{
				"type":        "string",
				"title":       "Password / app password",
				"description": fmt.Sprintf("Credentials for %s (%s). Sent only to this server, never stored.", acc.Email, mechanismLabel(acc.AuthMechanism)),
				"writeOnly":   true,
			},
		},
		"required": []string{"password"},
	}
	res, err := req.Session.Elicit(ctx, &mcp.ElicitParams{
		Mode:            "form",
		Message:         fmt.Sprintf("email-mcp needs credentials to access %s. Please enter the password or app password for %s.", acc.Email, acc.Username),
		RequestedSchema: schema,
	})
	if err != nil {
		return "", fmt.Errorf("elicitation failed: %w", err)
	}
	if res.Action != "accept" {
		return "", fmt.Errorf("user %s the credentials request", res.Action)
	}
	raw, _ := json.Marshal(res.Content)
	var form struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &form); err != nil || form.Password == "" {
		return "", fmt.Errorf("no password provided")
	}
	// Cache on the account so we only ask once per process.
	acc.Password = form.Password
	acc.PasswordEnv = ""
	return form.Password, nil
}

func mechanismLabel(m string) string {
	switch m {
	case "login":
		return "AUTH LOGIN"
	case "xoauth2":
		return "XOAUTH2 / OAuth2 access token"
	default:
		return "PLAIN"
	}
}

// withCredentials is kept for handlers that need the password value directly.
func withCredentials(sess *mail.Session, fn func(ctx context.Context, req *mcp.CallToolRequest) error) func(ctx context.Context, req *mcp.CallToolRequest) error {
	return func(ctx context.Context, req *mcp.CallToolRequest) error {
		if _, err := ensureCredentials(ctx, req, sess); err != nil {
			return err
		}
		return fn(ctx, req)
	}
}
