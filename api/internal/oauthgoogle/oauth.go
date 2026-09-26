// Package oauthgoogle speaks Google's OAuth 2.0 endpoints for connecting a
// YouTube channel: the authorization-code flow with PKCE, the refresh-token
// grant and token revocation. It is plain net/http (no oauth2 library) so
// every request goes through the caller's netguard client and an httptest
// double can stand in for Google in tests.
//
// Tokens never leave the server: the refresh token is sealed into the
// secrets table by the caller, and access tokens only live in memory
// inside a TokenSource.
package oauthgoogle

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Official endpoints; tests point Config at an httptest server instead.
const (
	DefaultAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	DefaultTokenURL  = "https://oauth2.googleapis.com/token"
	DefaultRevokeURL = "https://oauth2.googleapis.com/revoke"
)

// Scopes requested once at connect: upload, read the channel, and read
// analytics (used by the analytics phase through the same token).
var Scopes = []string{
	"https://www.googleapis.com/auth/youtube.upload",
	"https://www.googleapis.com/auth/youtube.readonly",
	"https://www.googleapis.com/auth/yt-analytics.readonly",
}

// UploadScope must be among the granted scopes for a channel to be usable.
const UploadScope = "https://www.googleapis.com/auth/youtube.upload"

// maxBody bounds how much of any token endpoint answer is read.
const maxBody = 64 << 10

// Config is one Google OAuth client. The redirect URL must match one
// registered for the client exactly; Google rejects anything else.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// HTTP makes every call; it should be a netguard client allowed to
	// reach Google's hosts only.
	HTTP *http.Client
	// Endpoint overrides, empty means the official endpoints.
	AuthURL   string
	TokenURL  string
	RevokeURL string
}

// Configured reports whether the client id, secret and redirect URL are
// all set; without them the connect flow is unavailable.
func (c *Config) Configured() bool {
	return c != nil && c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

// Token is a token endpoint answer. RefreshToken is empty on a refresh
// grant unless Google rotated it.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	// Scopes Google actually granted (the user may untick some).
	Scopes []string
}

// Error is an OAuth error answer. Code is Google's "error" field, e.g.
// invalid_grant when a refresh token was revoked or has expired.
type Error struct {
	Status      int
	Code        string
	Description string
}

func (e *Error) Error() string {
	return fmt.Sprintf("oauthgoogle: %d %s: %s", e.Status, e.Code, e.Description)
}

// ReconnectNeeded reports whether the grant itself is dead, so the channel
// must be connected again. API clients check this through an interface so
// they need not import this package.
func (e *Error) ReconnectNeeded() bool {
	return e.Code == "invalid_grant" || e.Code == "unauthorized_client"
}

// IsReconnectNeeded reports whether err says the refresh token is dead.
func IsReconnectNeeded(err error) bool {
	var oe *Error
	return errors.As(err, &oe) && oe.ReconnectNeeded()
}

// randomToken returns n random bytes, base64url without padding.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauthgoogle: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewState returns a fresh unguessable state value.
func NewState() (string, error) { return randomToken(32) }

// NewVerifier returns a PKCE code verifier (43 characters, within the
// RFC 7636 range of 43 to 128).
func NewVerifier() (string, error) { return randomToken(32) }

// StateHash is how a state value is stored: the raw state only ever
// exists in the redirect.
func StateHash(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}

// challenge is the S256 PKCE challenge for verifier.
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthCodeURL is where the browser goes to consent. access_type=offline
// and prompt=consent make Google return a refresh token every time, also
// on a reconnect.
func (c *Config) AuthCodeURL(state, verifier string) string {
	q := url.Values{
		"client_id":             {c.ClientID},
		"redirect_uri":          {c.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {strings.Join(Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge(verifier)},
		"code_challenge_method": {"S256"},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
	}
	return orDefault(c.AuthURL, DefaultAuthURL) + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

type errorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// postForm posts form to endpoint and decodes a token answer.
func (c *Config) postToken(ctx context.Context, form url.Values) (Token, error) {
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, orDefault(c.TokenURL, DefaultTokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("oauthgoogle: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Token{}, fmt.Errorf("oauthgoogle: read token answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Token{}, parseError(resp.StatusCode, body)
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Token{}, fmt.Errorf("oauthgoogle: decode token answer: %w", err)
	}
	if tr.AccessToken == "" {
		return Token{}, &Error{Status: resp.StatusCode, Code: "invalid_response", Description: "no access_token in answer"}
	}
	return Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		Scopes:       strings.Fields(tr.Scope),
	}, nil
}

func parseError(status int, body []byte) *Error {
	var er errorResponse
	_ = json.Unmarshal(body, &er)
	if er.Error == "" {
		er.Error = "http_error"
		er.ErrorDescription = http.StatusText(status)
	}
	return &Error{Status: status, Code: er.Error, Description: er.ErrorDescription}
}

// Exchange trades an authorization code for tokens, proving possession of
// the PKCE verifier. A missing refresh token is an error: without it the
// channel could not be used after the first hour.
func (c *Config) Exchange(ctx context.Context, code, verifier string) (Token, error) {
	tok, err := c.postToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {c.RedirectURL},
	})
	if err != nil {
		return Token{}, err
	}
	if tok.RefreshToken == "" {
		return Token{}, &Error{Status: http.StatusOK, Code: "no_refresh_token", Description: "Google returned no refresh token"}
	}
	return tok, nil
}

// Refresh gets a new access token from a refresh token.
func (c *Config) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	return c.postToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

// Revoke invalidates token (and, for a refresh token, every access token
// issued from it) at Google. A token Google no longer knows is already
// revoked, so invalid_token counts as success.
func (c *Config) Revoke(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, orDefault(c.RevokeURL, DefaultRevokeURL),
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("oauthgoogle: revoke request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	oe := parseError(resp.StatusCode, body)
	if oe.Code == "invalid_token" {
		return nil
	}
	return oe
}

// HasScope reports whether scope is among granted.
func HasScope(granted []string, scope string) bool {
	for _, s := range granted {
		if s == scope {
			return true
		}
	}
	return false
}
