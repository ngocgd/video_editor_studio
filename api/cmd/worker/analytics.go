package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"loomtale/api/internal/analytics"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/netguard"
	"loomtale/api/internal/oauthgoogle"
	"loomtale/api/internal/secrets"
	"loomtale/api/internal/youtube"
)

// analyticsHosts are the only hosts the analytics sync may dial: token
// refresh, the Data API, the Analytics API, and the Reporting API (which
// also serves the report downloads).
var analyticsHosts = []string{
	"oauth2.googleapis.com", "www.googleapis.com",
	"youtubeanalytics.googleapis.com", "youtubereporting.googleapis.com",
}

// analyticsSyncer builds the channel analytics syncer, or returns nil
// when no Google OAuth client is configured (channels cannot be
// connected, so there is nothing to sync).
func analyticsSyncer(cfg config, pool *pgxpool.Pool, queries *dbgen.Queries, store *secrets.Store) (*analytics.Syncer, error) {
	if cfg.GoogleClientID == "" {
		return nil, nil
	}
	if cfg.GoogleClientSecretPath == "" {
		return nil, fmt.Errorf("GOOGLE_CLIENT_ID is set: GOOGLE_CLIENT_SECRET_PATH is required too")
	}
	raw, err := os.ReadFile(cfg.GoogleClientSecretPath)
	if err != nil {
		return nil, fmt.Errorf("read GOOGLE_CLIENT_SECRET_PATH: %w", err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return nil, fmt.Errorf("GOOGLE_CLIENT_SECRET_PATH %s is empty", cfg.GoogleClientSecretPath)
	}
	oauth := &oauthgoogle.Config{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: secret,
		HTTP:         netguard.Client(netguard.Config{AllowedHosts: analyticsHosts}),
	}
	return &analytics.Syncer{
		Pool:    pool,
		Queries: queries,
		Clients: &analytics.OAuthClients{
			OAuth:   oauth,
			Secrets: store,
			Ledger:  &youtube.Ledger{Store: queries, Config: cfg.YouTubeQuota},
		},
	}, nil
}
