package oauthgoogle

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// expiryMargin refreshes an access token this long before Google would
// reject it, so a request in flight never carries a just-expired token.
const expiryMargin = time.Minute

// TokenSource hands out access tokens for one channel, refreshing from the
// stored refresh token when the cached one is close to expiry. Safe for
// concurrent use.
type TokenSource struct {
	Config       *Config
	RefreshToken string
	// OnRotate, when set, is called if Google issues a new refresh token so
	// the caller can re-seal it. Its error fails the token request.
	OnRotate func(ctx context.Context, refreshToken string) error

	mu     sync.Mutex
	access string
	expiry time.Time
}

// NewTokenSource returns a source seeded with tok's access token, so the
// first call after a code exchange needs no refresh round trip.
func NewTokenSource(cfg *Config, tok Token) *TokenSource {
	return &TokenSource{Config: cfg, RefreshToken: tok.RefreshToken, access: tok.AccessToken, expiry: tok.Expiry}
}

// Token returns a valid access token.
func (s *TokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.access != "" && time.Now().Add(expiryMargin).Before(s.expiry) {
		return s.access, nil
	}
	if s.RefreshToken == "" {
		return "", &Error{Code: "invalid_grant", Description: "no refresh token stored"}
	}
	tok, err := s.Config.Refresh(ctx, s.RefreshToken)
	if err != nil {
		return "", err
	}
	if tok.RefreshToken != "" && tok.RefreshToken != s.RefreshToken {
		if s.OnRotate != nil {
			if err := s.OnRotate(ctx, tok.RefreshToken); err != nil {
				return "", err
			}
		}
		s.RefreshToken = tok.RefreshToken
	}
	s.access, s.expiry = tok.AccessToken, tok.Expiry
	return s.access, nil
}

// Transport authorizes each request with a Bearer access token from
// Source. A dead grant surfaces as an *Error whose ReconnectNeeded is true.
type Transport struct {
	Source *TokenSource
	// Base carries the request; nil means http.DefaultTransport.
	Base http.RoundTripper
}

// RoundTrip implements http.RoundTripper. The request is cloned before
// the header is set, as the RoundTripper contract requires.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Source == nil {
		return nil, errors.New("oauthgoogle: transport has no token source")
	}
	tok, err := t.Source.Token(req.Context())
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+tok)
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

// Client returns an *http.Client that authorizes requests through source,
// dialing with base's transport (the netguard client's). The base client's
// timeout is kept.
func Client(base *http.Client, source *TokenSource) *http.Client {
	var rt http.RoundTripper
	var timeout time.Duration
	if base != nil {
		rt, timeout = base.Transport, base.Timeout
	}
	return &http.Client{Transport: &Transport{Source: source, Base: rt}, Timeout: timeout}
}
