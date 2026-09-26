package analytics

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"loomtale/api/internal/oauthgoogle"
	"loomtale/api/internal/secrets"
	"loomtale/api/internal/youtube"
)

// Clients are one channel's authorized Google API clients.
type Clients struct {
	// Data reads channel and video metadata (Data API quota).
	Data *youtube.Client
	// Analytics runs reports.query and the Reporting API calls.
	Analytics *youtube.AnalyticsClient
}

// ClientFactory builds the clients of one connected channel.
type ClientFactory interface {
	ForChannel(ctx context.Context, tenantID, channelID uuid.UUID) (Clients, error)
}

// OAuthClients builds channel clients from the channel's sealed refresh
// token. The OAuth config's HTTP client is the netguard client, so every
// call (including report downloads) only dials allowed Google hosts.
type OAuthClients struct {
	OAuth   *oauthgoogle.Config
	Secrets *secrets.Store
	Ledger  *youtube.Ledger
	// Base URLs default to the official endpoints; tests point them at
	// httptest doubles.
	APIBase, AnalyticsBase, ReportingBase string
}

// errNoRefreshToken is reported as a dead grant: the channel has to be
// connected again before it can sync.
var errNoRefreshToken = &youtube.APIError{
	Kind: youtube.KindAuth, Reason: youtube.ReasonReconnectNeeded, Message: "no refresh token stored for the channel",
}

// ForChannel opens the channel's refresh token and returns clients that
// authorize as that channel. A rotated refresh token is sealed again.
func (f *OAuthClients) ForChannel(ctx context.Context, tenantID, channelID uuid.UUID) (Clients, error) {
	owner := channelID.String()
	refresh, err := f.Secrets.Open(ctx, tenantID, secrets.KindYouTubeRefresh, owner)
	if errors.Is(err, secrets.ErrNotFound) {
		return Clients{}, errNoRefreshToken
	}
	if err != nil {
		return Clients{}, fmt.Errorf("analytics: open channel token: %w", err)
	}
	src := &oauthgoogle.TokenSource{
		Config:       f.OAuth,
		RefreshToken: refresh,
		OnRotate: func(ctx context.Context, rotated string) error {
			return f.Secrets.Put(ctx, tenantID, secrets.KindYouTubeRefresh, owner, rotated)
		},
	}
	hc := oauthgoogle.Client(f.OAuth.HTTP, src)
	return Clients{
		Data:      &youtube.Client{HTTP: hc, Ledger: f.Ledger, APIBase: f.APIBase},
		Analytics: &youtube.AnalyticsClient{HTTP: hc, AnalyticsBase: f.AnalyticsBase, ReportingBase: f.ReportingBase},
	}, nil
}
