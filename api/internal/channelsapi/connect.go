package channelsapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/oauthgoogle"
	"loomtale/api/internal/secrets"
	"loomtale/api/internal/tenant"
	"loomtale/api/internal/youtube"
)

// stateTTL bounds how long a consent handshake may take.
const stateTTL = 10 * time.Minute

// settingsPath is where the callback sends the browser back to.
const settingsPath = "/settings/youtube"

// StartYouTubeConnect implements gen.StrictServerInterface: it records a
// single-use state bound to this session with its PKCE verifier and
// returns Google's consent URL.
func (h *ChannelsAPI) StartYouTubeConnect(ctx context.Context, _ gen.StartYouTubeConnectRequestObject) (gen.StartYouTubeConnectResponseObject, error) {
	if !h.oauthConfigured() {
		detail := "set GOOGLE_CLIENT_ID, GOOGLE_OAUTH_REDIRECT_URL and the client secret file to connect YouTube channels"
		return gen.StartYouTubeConnect503ApplicationProblemPlusJSONResponse{
			Title: "Google OAuth is not configured", Status: 503, Detail: &detail,
		}, nil
	}
	info := tenant.MustFromCtx(ctx)
	sess, ok := authpkg.FromCtx(ctx)
	if !ok {
		return nil, errors.New("channelsapi: no session in context")
	}
	if err := h.Queries.DeleteExpiredYouTubeOAuthStates(ctx); err != nil {
		slog.WarnContext(ctx, "channelsapi: prune expired oauth states", "error", err)
	}
	state, err := oauthgoogle.NewState()
	if err != nil {
		return nil, err
	}
	verifier, err := oauthgoogle.NewVerifier()
	if err != nil {
		return nil, err
	}
	if err := h.Queries.CreateYouTubeOAuthState(ctx, dbgen.CreateYouTubeOAuthStateParams{
		StateHash:    oauthgoogle.StateHash(state),
		TenantID:     idconv.ToPg(info.ID),
		UserID:       idconv.ToPg(sess.UserID),
		SessionID:    idconv.ToPg(sess.ID),
		CodeVerifier: verifier,
		ExpiresAt:    idconv.ToPgTimestamptz(time.Now().Add(stateTTL)),
	}); err != nil {
		return nil, fmt.Errorf("channelsapi: store oauth state: %w", err)
	}
	return gen.StartYouTubeConnect200JSONResponse{AuthorizationUrl: h.OAuth.AuthCodeURL(state, verifier)}, nil
}

// connectError is a short, stable reason code shown by the Settings page.
// Codes are fixed strings: nothing from the query or from Google is
// reflected into the redirect.
type connectError string

const (
	errConsentDenied  connectError = "consent_denied"
	errInvalidRequest connectError = "invalid_request"
	errInvalidState   connectError = "invalid_state"
	errNotConfigured  connectError = "not_configured"
	errExchange       connectError = "exchange_failed"
	errUploadScope    connectError = "upload_scope_missing"
	errNoChannel      connectError = "no_channel"
	errQuota          connectError = "quota_exceeded"
	errChannelRead    connectError = "channel_read_failed"
	errStore          connectError = "store_failed"
)

func redirect(q url.Values) gen.YouTubeOAuthCallback302Response {
	return gen.YouTubeOAuthCallback302Response{Headers: gen.YouTubeOAuthCallback302ResponseHeaders{
		Location: settingsPath + "?" + q.Encode(),
	}}
}

func fail(code connectError) gen.YouTubeOAuthCallback302Response {
	return redirect(url.Values{"connect": {"error"}, "reason": {string(code)}})
}

// YouTubeOAuthCallback implements gen.StrictServerInterface. Google sends
// the browser here after consent. The state is consumed first (single
// use, whatever happens next), then the code is exchanged with the PKCE
// verifier, the channel is read and its refresh token sealed.
func (h *ChannelsAPI) YouTubeOAuthCallback(ctx context.Context, request gen.YouTubeOAuthCallbackRequestObject) (gen.YouTubeOAuthCallbackResponseObject, error) {
	info := tenant.MustFromCtx(ctx)
	sess, ok := authpkg.FromCtx(ctx)
	if !ok {
		return nil, errors.New("channelsapi: no session in context")
	}
	p := request.Params
	if p.State == nil || *p.State == "" {
		return fail(errInvalidRequest), nil
	}
	st, err := h.Queries.ConsumeYouTubeOAuthState(ctx, dbgen.ConsumeYouTubeOAuthStateParams{
		StateHash: oauthgoogle.StateHash(*p.State),
		TenantID:  idconv.ToPg(info.ID),
		SessionID: idconv.ToPg(sess.ID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fail(errInvalidState), nil
	}
	if err != nil {
		return nil, fmt.Errorf("channelsapi: consume oauth state: %w", err)
	}
	if p.Error != nil && *p.Error != "" {
		if *p.Error == "access_denied" {
			return fail(errConsentDenied), nil
		}
		return fail(errExchange), nil
	}
	if p.Code == nil || *p.Code == "" {
		return fail(errInvalidRequest), nil
	}
	if !h.oauthConfigured() {
		return fail(errNotConfigured), nil
	}

	tok, err := h.OAuth.Exchange(ctx, *p.Code, st.CodeVerifier)
	if err != nil {
		slog.WarnContext(ctx, "channelsapi: oauth code exchange failed", "error", err)
		return fail(errExchange), nil
	}
	// From here on a failure must not leave a live grant behind.
	abandon := func(code connectError) (gen.YouTubeOAuthCallbackResponseObject, error) {
		if err := h.OAuth.Revoke(ctx, tok.RefreshToken); err != nil {
			slog.WarnContext(ctx, "channelsapi: revoke abandoned grant", "error", err)
		}
		return fail(code), nil
	}
	if !oauthgoogle.HasScope(tok.Scopes, oauthgoogle.UploadScope) {
		return abandon(errUploadScope)
	}

	yt := &youtube.Client{
		HTTP:    oauthgoogle.Client(h.OAuth.HTTP, oauthgoogle.NewTokenSource(h.OAuth, tok)),
		Ledger:  h.Ledger,
		APIBase: h.YouTubeAPIBase,
	}
	ch, err := yt.MyChannel(ctx)
	switch {
	case errors.Is(err, youtube.ErrNoChannel):
		return abandon(errNoChannel)
	case youtube.IsKind(err, youtube.KindQuota):
		return abandon(errQuota)
	case err != nil:
		slog.WarnContext(ctx, "channelsapi: read channel after consent", "error", err)
		return abandon(errChannelRead)
	}

	row, err := h.Queries.UpsertYouTubeChannel(ctx, dbgen.UpsertYouTubeChannelParams{
		ID:                idconv.ToPg(idconv.NewV7()),
		TenantID:          idconv.ToPg(info.ID),
		YoutubeChannelID:  ch.ID,
		Title:             ch.Title,
		ThumbnailUrl:      ch.ThumbnailURL,
		Scopes:            tok.Scopes,
		LongUploadsStatus: ch.LongUploadsStatus,
		ConnectedBy:       idconv.ToPg(sess.UserID),
	})
	if err != nil {
		slog.ErrorContext(ctx, "channelsapi: store channel", "error", err)
		return abandon(errStore)
	}
	channelID := idconv.FromPg(row.ID)
	if err := h.Secrets.Put(ctx, info.ID, secrets.KindYouTubeRefresh, channelID.String(), tok.RefreshToken); err != nil {
		slog.ErrorContext(ctx, "channelsapi: seal refresh token", "error", err)
		h.markReconnectNeeded(ctx, info.ID, channelID)
		return abandon(errStore)
	}
	h.recordAudit(ctx, "youtube_channel.connect", channelID, map[string]any{
		"youtube_channel_id":  ch.ID,
		"scopes":              tok.Scopes,
		"long_uploads_status": ch.LongUploadsStatus,
	})
	return redirect(url.Values{"connect": {"ok"}, "channel": {channelID.String()}}), nil
}

// markReconnectNeeded flags a channel whose token could not be stored,
// so it is not shown as usable.
func (h *ChannelsAPI) markReconnectNeeded(ctx context.Context, tenantID, channelID uuid.UUID) {
	if _, err := h.Queries.SetYouTubeChannelStatus(ctx, dbgen.SetYouTubeChannelStatusParams{
		Status: "reconnect_needed", TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(channelID),
	}); err != nil {
		slog.ErrorContext(ctx, "channelsapi: mark reconnect needed", "error", err)
	}
}
