package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"loomtale/api/internal/netguard"
	"loomtale/api/internal/oauthgoogle"
)

// googleHosts are the only hosts the Google OAuth and Data API client may
// dial: the token/revoke endpoints and the Data API (accounts.google.com
// is only ever visited by the browser).
var googleHosts = []string{"oauth2.googleapis.com", "www.googleapis.com"}

// googleOAuthConfig builds the Google OAuth client from config. With no
// client id it returns an unconfigured client (connecting is disabled,
// listing still works); a half-configured client is a startup error so a
// typo is not discovered at the first connect.
func googleOAuthConfig(cfg config) (*oauthgoogle.Config, error) {
	hc := netguard.Client(netguard.Config{AllowedHosts: googleHosts})
	if cfg.GoogleClientID == "" {
		return &oauthgoogle.Config{HTTP: hc}, nil
	}
	if cfg.GoogleClientSecretPath == "" || cfg.GoogleOAuthRedirectURL == "" {
		return nil, fmt.Errorf("GOOGLE_CLIENT_ID is set: GOOGLE_CLIENT_SECRET_PATH and GOOGLE_OAUTH_REDIRECT_URL are required too")
	}
	u, err := url.Parse(cfg.GoogleOAuthRedirectURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		!strings.HasSuffix(u.Path, "/api/v1/channels/oauth/callback") {
		return nil, fmt.Errorf("GOOGLE_OAUTH_REDIRECT_URL must be an absolute URL ending in /api/v1/channels/oauth/callback, got %q", cfg.GoogleOAuthRedirectURL)
	}
	raw, err := os.ReadFile(cfg.GoogleClientSecretPath)
	if err != nil {
		return nil, fmt.Errorf("read GOOGLE_CLIENT_SECRET_PATH: %w", err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return nil, fmt.Errorf("GOOGLE_CLIENT_SECRET_PATH %s is empty", cfg.GoogleClientSecretPath)
	}
	return &oauthgoogle.Config{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: secret,
		RedirectURL:  cfg.GoogleOAuthRedirectURL,
		HTTP:         hc,
	}, nil
}
