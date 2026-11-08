// Package oauthserver implements a self-contained OAuth 2.0 authorization
// server for email-mcp: the server is both the MCP protected resource and
// its own authorization server (RFC 8414 metadata, RFC 7591 dynamic client
// registration, authorization-code + PKCE flow, refresh tokens, JWKS).
//
// After consent, the issued access token is an RS256 JWT whose "account"
// claim is the full mail-account description, AES-256-GCM encrypted with the
// deployment secret. The MCP endpoint verifies the JWT signature and decrypts
// the account statelessly — one bearer token per MCP connection carries all
// the connection info, which is what clients with a single auth header
// (e.g. Mistral Vibe) need.
package oauthserver

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"email-mcp/internal/config"
)

// Defaults and well-known paths of the authorization server.
const (
	DefaultTokenTTL    = 24 * time.Hour
	DefaultAuthCodeTTL = 10 * time.Minute
	DefaultRefreshTTL  = 30 * 24 * time.Hour

	MetadataPath         = "/.well-known/oauth-authorization-server"
	JWKSPath             = "/.well-known/jwks.json"
	RegisterPath         = "/oauth/register"
	AuthorizePath        = "/oauth/authorize"
	TokenPath            = "/oauth/token"
	ResourceMetadataPath = "/.well-known/oauth-protected-resource"
)

// Server is the OAuth 2.0 authorization server. It is safe for concurrent use.
type Server struct {
	baseURL string // e.g. https://mcp.example.org (no trailing slash)

	key   *rsa.PrivateKey
	keyID string
	aead  cipher.AEAD // encrypts the account claim in access tokens

	tokenTTL   time.Duration
	refreshTTL time.Duration

	mu        sync.Mutex
	clients   map[string]*clientReg         // client_id → registration
	authCodes map[string]*authorizationCode // code → pending grant
	refreshes map[string]*refreshToken      // refresh token → grant
}

type clientReg struct {
	ClientID     string
	ClientSecret string
	RedirectURIs []string
	CreatedAt    time.Time
}

type authorizationCode struct {
	ClientID    string
	RedirectURI string
	Account     *config.Account // captured at consent time
	Scope       string
	Challenge   string // PKCE S256 code_challenge
	ExpiresAt   time.Time
	Used        bool
}

type refreshToken struct {
	ClientID  string
	Account   *config.Account
	Scope     string
	ExpiresAt time.Time
}

// Options configures New.
type Options struct {
	// BaseURL is the public base URL of this server, e.g.
	// "https://mcp.example.org" (no trailing slash).
	BaseURL string
	// SigningKeyPEM is the PEM-encoded RSA private key (PKCS#8 or PKCS#1)
	// used to sign access tokens. Required: the key must be stable across
	// restarts, otherwise all previously issued tokens are rejected
	// ("unknown kid") after a redeploy.
	SigningKeyPEM []byte
	// TokenSecret is the required 32-byte secret used to encrypt the
	// account claim inside access tokens. It must be stable across
	// restarts, otherwise all previously issued tokens fail decryption.
	TokenSecret []byte
	// TokenTTL is the access-token lifetime (default 24h).
	TokenTTL time.Duration
	// RefreshTTL is the refresh-token lifetime (default 30d).
	RefreshTTL time.Duration
}

// ParseSigningKeyPEM decodes a PEM-encoded RSA private key, trying PKCS#8
// ("PRIVATE KEY") first, then PKCS#1 ("RSA PRIVATE KEY").
func ParseSigningKeyPEM(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("oauthserver: signing key is not PEM-encoded")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("oauthserver: PKCS#8 key is %T, want RSA", key)
		}
		return rsaKey, nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("oauthserver: parse signing key (PKCS#8/PKCS#1): %w", err)
	}
	return key, nil
}

// New creates the authorization server. The RSA signing key must come from
// opts.SigningKeyPEM and the 32-byte token secret from opts.TokenSecret so
// that issued tokens survive restarts. BaseURL may be empty: the issuer is
// then derived per request from the request's scheme+host.
func New(opts *Options) (*Server, error) {
	if opts == nil {
		opts = &Options{}
	}
	if len(opts.SigningKeyPEM) == 0 {
		return nil, errors.New("oauthserver: SigningKeyPEM is required (set EMAIL_MCP_SIGNING_KEY or EMAIL_MCP_SIGNING_KEY_FILE); tokens would not survive a restart")
	}
	key, err := ParseSigningKeyPEM(opts.SigningKeyPEM)
	if err != nil {
		return nil, err
	}
	secret := opts.TokenSecret
	if len(secret) != 32 {
		return nil, fmt.Errorf("oauthserver: TokenSecret must be 32 bytes (set EMAIL_MCP_TOKEN_SECRET), got %d", len(secret))
	}
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(key.PublicKey.N.Bytes())
	s := &Server{
		baseURL:    opts.BaseURL,
		key:        key,
		keyID:      base64.RawURLEncoding.EncodeToString(sum[:8]),
		aead:       aead,
		tokenTTL:   opts.TokenTTL,
		refreshTTL: opts.RefreshTTL,
		clients:    map[string]*clientReg{},
		authCodes:  map[string]*authorizationCode{},
		refreshes:  map[string]*refreshToken{},
	}
	if s.tokenTTL == 0 {
		s.tokenTTL = DefaultTokenTTL
	}
	if s.refreshTTL == 0 {
		s.refreshTTL = DefaultRefreshTTL
	}
	return s, nil
}

// BaseURL returns the configured public base URL.
func (s *Server) BaseURL() string { return s.baseURL }

// Issuer returns the configured issuer; empty when derived per request.
func (s *Server) Issuer() string { return s.baseURL }

// accountWire is the serialized form of an account inside the encrypted JWT
// claim. Unlike config.Account's default JSON (which omits nothing), this
// uses explicit field names so ports/flags survive even when zero.
type accountWire struct {
	Email         string   `json:"email"`
	DisplayName   string   `json:"display_name,omitempty"`
	Username      string   `json:"username,omitempty"`
	Password      string   `json:"password,omitempty"`
	AuthMechanism string   `json:"auth_mechanism,omitempty"`
	IMAP          IMAPWire `json:"imap"`
	SMTP          SMTPWire `json:"smtp"`
	SentMailbox   string   `json:"sent_mailbox,omitempty"`
	DraftsMailbox string   `json:"drafts_mailbox,omitempty"`
}

type IMAPWire struct {
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	TLS                bool   `json:"tls,omitempty"`
	Insecure           bool   `json:"insecure,omitempty"`
	InsecureSkipVerify bool   `json:"skip_verify,omitempty"`
}

type SMTPWire struct {
	Host               string `json:"host,omitempty"`
	Port               int    `json:"port,omitempty"`
	TLS                bool   `json:"tls,omitempty"`
	Insecure           bool   `json:"insecure,omitempty"`
	InsecureSkipVerify bool   `json:"skip_verify,omitempty"`
}

func toWire(acc *config.Account) accountWire {
	return accountWire{
		Email: acc.Email, DisplayName: acc.DisplayName, Username: acc.Username,
		Password: acc.Password, AuthMechanism: acc.AuthMechanism,
		IMAP: IMAPWire{Host: acc.IMAP.Host, Port: acc.IMAP.Port, TLS: acc.IMAP.TLS,
			Insecure: acc.IMAP.Insecure, InsecureSkipVerify: acc.IMAP.InsecureSkipVerify},
		SMTP: SMTPWire{Host: acc.SMTP.Host, Port: acc.SMTP.Port, TLS: acc.SMTP.TLS,
			Insecure: acc.SMTP.Insecure, InsecureSkipVerify: acc.SMTP.InsecureSkipVerify},
		SentMailbox: acc.SentMailbox, DraftsMailbox: acc.DraftsMailbox,
	}
}

func fromWire(w accountWire) *config.Account {
	return &config.Account{
		Email: w.Email, DisplayName: w.DisplayName, Username: w.Username,
		Password: w.Password, AuthMechanism: w.AuthMechanism,
		IMAP: config.IMAPConfig{Host: w.IMAP.Host, Port: w.IMAP.Port, TLS: w.IMAP.TLS,
			Insecure: w.IMAP.Insecure, InsecureSkipVerify: w.IMAP.InsecureSkipVerify},
		SMTP: config.SMTPConfig{Host: w.SMTP.Host, Port: w.SMTP.Port, TLS: w.SMTP.TLS,
			Insecure: w.SMTP.Insecure, InsecureSkipVerify: w.SMTP.InsecureSkipVerify},
		SentMailbox: w.SentMailbox, DraftsMailbox: w.DraftsMailbox,
	}
}

// encryptAccount seals the account JSON into an opaque string for the JWT.
func (s *Server) encryptAccount(acc *config.Account) (string, error) {
	raw, err := json.Marshal(toWire(acc))
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, raw, nil)), nil
}

// decryptAccount opens an encrypted account claim back into an Account.
func (s *Server) decryptAccount(opaque string) (*config.Account, error) {
	raw, err := base64.RawURLEncoding.DecodeString(opaque)
	if err != nil {
		return nil, err
	}
	if len(raw) < s.aead.NonceSize() {
		return nil, errors.New("token claim too short")
	}
	nonce, ct := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("token claim decryption failed")
	}
	var w accountWire
	if err := json.Unmarshal(plain, &w); err != nil {
		return nil, err
	}
	return fromWire(w), nil
}

// issueTokens creates the access-token JWT (with encrypted account claim) and
// a refresh token for the account.
func (s *Server) issueTokens(clientID string, acc *config.Account, scope string) (access, refresh string, expiresIn int64, err error) {
	enc, err := s.encryptAccount(acc)
	if err != nil {
		return "", "", 0, err
	}
	now := time.Now()
	exp := now.Add(s.tokenTTL)
	issuer := s.baseURL
	if issuer == "" {
		issuer = "http://localhost" // per-request derived; verification uses the configured issuer only
	}
	claims := jwt.MapClaims{
		"iss":     issuer,
		"sub":     acc.Email,
		"aud":     s.baseURL + "/mcp",
		"iat":     now.Unix(),
		"exp":     exp.Unix(),
		"scope":   scope,
		"account": enc,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.keyID
	access, err = tok.SignedString(s.key)
	if err != nil {
		return "", "", 0, err
	}
	refresh = randomToken(32)
	s.mu.Lock()
	s.refreshes[refresh] = &refreshToken{
		ClientID:  clientID,
		Account:   acc,
		Scope:     scope,
		ExpiresAt: now.Add(s.refreshTTL),
	}
	s.mu.Unlock()
	return access, refresh, int64(s.tokenTTL.Seconds()), nil
}

// VerifyAccessToken verifies an RS256 JWT access token and returns the
// embedded account.
func (s *Server) VerifyAccessToken(_ context.Context, token string) (*config.Account, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		if kid, _ := t.Header["kid"].(string); kid != s.keyID {
			return nil, errors.New("unknown kid")
		}
		return &s.key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	// When a fixed issuer is configured, enforce it.
	if s.baseURL != "" {
		if iss, _ := mc0(parsed, "iss"); iss != s.baseURL {
			return nil, errors.New("issuer mismatch")
		}
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, errors.New("bad claims")
	}
	enc, _ := mc["account"].(string)
	if enc == "" {
		return nil, errors.New("token has no account claim")
	}
	return s.decryptAccount(enc)
}

func mc0(tok *jwt.Token, key string) (string, bool) {
	mc, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return "", false
	}
	v, ok := mc[key].(string)
	return v, ok
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
