package oauthserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"email-mcp/internal/config"
)

// Handler returns the http.Handler serving all OAuth endpoints of the
// authorization server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+MetadataPath, s.serveMetadata)
	mux.HandleFunc("GET "+JWKSPath, s.serveJWKS)
	mux.HandleFunc("POST "+RegisterPath, s.serveRegister)
	mux.HandleFunc("GET "+AuthorizePath, s.serveAuthorizeForm)
	mux.HandleFunc("POST "+AuthorizePath, s.serveAuthorizeConsent)
	mux.HandleFunc("POST "+TokenPath, s.serveToken)
	return mux
}

// issuerFor derives the public issuer/base URL: the configured BaseURL when
// set (EMAIL_MCP_BASE_URL), else the request's scheme+host.
func (s *Server) issuerFor(r *http.Request) string {
	if s.baseURL != "" {
		return s.baseURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	return scheme + "://" + r.Host
}

func (s *Server) serveMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.issuerFor(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"issuer":                                base,
		"authorization_endpoint":                base + AuthorizePath,
		"token_endpoint":                        base + TokenPath,
		"registration_endpoint":                 base + RegisterPath,
		"jwks_uri":                              base + JWKSPath,
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"email"},
	})
}

func (s *Server) serveJWKS(w http.ResponseWriter, r *http.Request) {
	pub := &s.key.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(bigEndian(pub.E))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": s.keyID,
			"n":   n,
			"e":   e,
		}},
	})
}

func bigEndian(e int) []byte {
	// RSA public exponent, big-endian minimal bytes.
	b := []byte{byte(e >> 24), byte(e >> 16), byte(e >> 8), byte(e)}
	i := 0
	for i < len(b)-1 && b[i] == 0 {
		i++
	}
	return b[i:]
}

// serveRegister implements RFC 7591 dynamic client registration.
func (s *Server) serveRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		ClientName              string   `json:"client_name"`
		Scope                   string   `json:"scope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "bad registration body: "+err.Error())
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "redirect_uris required")
		return
	}
	for _, u := range req.RedirectURIs {
		pu, err := url.Parse(u)
		if err != nil || pu.Scheme == "" || pu.Host == "" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid redirect_uri "+u)
			return
		}
	}
	clientID := randomToken(16)
	clientSecret := randomToken(32)
	s.mu.Lock()
	s.clients[clientID] = &clientReg{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURIs: req.RedirectURIs,
		CreatedAt:    time.Now(),
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"client_id":                  clientID,
		"client_secret":              clientSecret,
		"client_id_issued_at":        time.Now().Unix(),
		"redirect_uris":              req.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "client_secret_basic",
		"scope":                      "email",
	})
}

// authorizeQuery holds the OAuth authorize request parameters echoed into the
// consent form as hidden fields.
type authorizeQuery struct {
	ClientID            string
	RedirectURI         string
	State               string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
}

var consentPage = template.Must(template.New("consent").Parse(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>email-mcp — authorize mail access</title></head>
<body>
<h1>Authorize email-mcp to access your mail account</h1>
<p>Client <code>{{.ClientID}}</code> requests access (scope: {{.Scope}}).
Enter the mail account to grant:</p>
<form method="post" action="` + AuthorizePath + `">
<input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="scope" value="{{.Scope}}">
<input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
<input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
<fieldset>
<legend>Account</legend>
<p><label>email (required): <input type="text" name="email" placeholder="user@gmail.com" required></label></p>
<p><label>password / app password (required): <input type="password" name="password" required></label></p>
<p><label>username (login override, default: the email): <input type="text" name="username"></label></p>
<p><label>display_name: <input type="text" name="display_name"></label></p>
<p><label>auth_mechanism:
<select name="auth_mechanism">
<option value="">plain (default)</option>
<option value="login">login</option>
<option value="xoauth2">xoauth2</option>
</select></label></p>
</fieldset>
<fieldset>
<legend>IMAP (leave empty for DNS auto-discovery)</legend>
<p><label>imap_host: <input type="text" name="imap_host" placeholder="imap.example.org"></label>
<label>imap_port: <input type="text" name="imap_port" size="6" placeholder="993"></label></p>
<p><label><input type="checkbox" name="imap_tls" value="1"> imap_tls</label>
<label><input type="checkbox" name="imap_starttls" value="1"> imap_starttls</label>
<label><input type="checkbox" name="imap_insecure" value="1"> imap_insecure (local testing only)</label>
<label><input type="checkbox" name="imap_skip_verify" value="1"> imap_skip_verify (local testing only)</label></p>
</fieldset>
<fieldset>
<legend>SMTP (leave empty for DNS auto-discovery)</legend>
<p><label>smtp_host: <input type="text" name="smtp_host" placeholder="smtp.example.org"></label>
<label>smtp_port: <input type="text" name="smtp_port" size="6" placeholder="465"></label></p>
<p><label><input type="checkbox" name="smtp_tls" value="1"> smtp_tls</label>
<label><input type="checkbox" name="smtp_starttls" value="1"> smtp_starttls</label>
<label><input type="checkbox" name="smtp_insecure" value="1"> smtp_insecure (local testing only)</label>
<label><input type="checkbox" name="smtp_skip_verify" value="1"> smtp_skip_verify (local testing only)</label></p>
</fieldset>
<fieldset>
<legend>Folders</legend>
<p><label>sent_mailbox: <input type="text" name="sent_mailbox" placeholder="Sent"></label>
<label>drafts_mailbox: <input type="text" name="drafts_mailbox" placeholder="Drafts"></label></p>
</fieldset>
<p><button type="submit">Authorize</button></p>
</form>
</body>
</html>
`))

func parseAuthorizeQuery(r *http.Request) (authorizeQuery, error) {
	q := r.URL.Query()
	aq := authorizeQuery{
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		State:               q.Get("state"),
		Scope:               q.Get("scope"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
	}
	if aq.ClientID == "" {
		return aq, fmt.Errorf("missing client_id")
	}
	if aq.RedirectURI == "" {
		return aq, fmt.Errorf("missing redirect_uri")
	}
	if aq.CodeChallenge == "" || aq.CodeChallengeMethod != "S256" {
		return aq, fmt.Errorf("PKCE code_challenge (S256) is required")
	}
	return aq, nil
}

func (s *Server) serveAuthorizeForm(w http.ResponseWriter, r *http.Request) {
	aq, err := parseAuthorizeQuery(r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.mu.Lock()
	reg := s.clients[aq.ClientID]
	s.mu.Unlock()
	if reg == nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}
	if !validRedirect(reg.RedirectURIs, aq.RedirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri not registered")
		return
	}
	if aq.Scope == "" {
		aq.Scope = "email"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	consentPage.Execute(w, aq) //nolint:errcheck
}

// accountFromForm builds an account from the consent form fields.
func accountFromForm(r *http.Request) (*config.Account, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return config.AccountFromFormValues(r.PostForm)
}

func (s *Server) serveAuthorizeConsent(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	// The hidden fields come back in the POST body.
	aq := authorizeQuery{
		ClientID:            r.PostFormValue("client_id"),
		RedirectURI:         r.PostFormValue("redirect_uri"),
		State:               r.PostFormValue("state"),
		Scope:               r.PostFormValue("scope"),
		CodeChallenge:       r.PostFormValue("code_challenge"),
		CodeChallengeMethod: r.PostFormValue("code_challenge_method"),
	}
	if aq.ClientID == "" || aq.RedirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing client_id or redirect_uri")
		return
	}
	if aq.CodeChallenge == "" || aq.CodeChallengeMethod != "S256" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "PKCE code_challenge (S256) is required")
		return
	}
	if aq.Scope == "" {
		aq.Scope = "email"
	}
	s.mu.Lock()
	reg := s.clients[aq.ClientID]
	s.mu.Unlock()
	if reg == nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}
	if !validRedirect(reg.RedirectURIs, aq.RedirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri not registered")
		return
	}
	acc, err := accountFromForm(r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	code := randomToken(24)
	s.mu.Lock()
	s.authCodes[code] = &authorizationCode{
		ClientID:    aq.ClientID,
		RedirectURI: aq.RedirectURI,
		Account:     acc,
		Scope:       aq.Scope,
		Challenge:   aq.CodeChallenge,
		ExpiresAt:   time.Now().Add(DefaultAuthCodeTTL),
	}
	s.mu.Unlock()
	u, _ := url.Parse(aq.RedirectURI)
	q := u.Query()
	q.Set("code", code)
	if aq.State != "" {
		q.Set("state", aq.State)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// serveToken implements the token endpoint (authorization_code and
// refresh_token grants).
func (s *Server) serveToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	grant := r.PostFormValue("grant_type")
	switch grant {
	case "authorization_code":
		s.handleAuthCodeGrant(w, r)
	case "refresh_token":
		s.handleRefreshGrant(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

// authenticateClient checks client credentials (Basic or POST body).
func (s *Server) authenticateClient(r *http.Request) (*clientReg, error) {
	id, secret, hasBasic := r.BasicAuth()
	if !hasBasic {
		id = r.PostFormValue("client_id")
		secret = r.PostFormValue("client_secret")
	}
	s.mu.Lock()
	reg := s.clients[id]
	s.mu.Unlock()
	if reg == nil || secret == "" || reg.ClientSecret != secret {
		return nil, fmt.Errorf("invalid client credentials")
	}
	return reg, nil
}

func (s *Server) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	reg, err := s.authenticateClient(r)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}
	code := r.PostFormValue("code")
	verifier := r.PostFormValue("code_verifier")
	redirectURI := r.PostFormValue("redirect_uri")
	s.mu.Lock()
	ac := s.authCodes[code]
	s.mu.Unlock()
	if ac == nil || ac.Used || time.Now().After(ac.ExpiresAt) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "bad or expired code")
		return
	}
	if ac.ClientID != reg.ClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code was issued to another client")
		return
	}
	if redirectURI != "" && redirectURI != ac.RedirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	// PKCE S256 verification.
	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != ac.Challenge {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	s.mu.Lock()
	ac.Used = true
	delete(s.authCodes, code)
	s.mu.Unlock()
	access, refresh, expiresIn, err := s.issueTokens(reg.ClientID, ac.Account, ac.Scope)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeTokenResponse(w, access, refresh, expiresIn)
}

func (s *Server) handleRefreshGrant(w http.ResponseWriter, r *http.Request) {
	reg, err := s.authenticateClient(r)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}
	rt := r.PostFormValue("refresh_token")
	s.mu.Lock()
	token := s.refreshes[rt]
	s.mu.Unlock()
	if token == nil || token.ClientID != reg.ClientID || time.Now().After(token.ExpiresAt) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "bad or expired refresh token")
		return
	}
	access, newRefresh, expiresIn, err := s.issueTokens(reg.ClientID, token.Account, token.Scope)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	// Rotate the refresh token.
	s.mu.Lock()
	delete(s.refreshes, rt)
	s.mu.Unlock()
	writeTokenResponse(w, access, newRefresh, expiresIn)
}

func validRedirect(registered []string, redirect string) bool {
	for _, u := range registered {
		if u == redirect {
			return true
		}
	}
	return false
}

func writeTokenResponse(w http.ResponseWriter, access, refresh string, expiresIn int64) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    expiresIn,
		"refresh_token": refresh,
		"scope":         "email",
	})
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"error":             code,
		"error_description": desc,
	})
}
