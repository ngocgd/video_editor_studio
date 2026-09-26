//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/channelsapi"
	"loomtale/api/internal/crypto/envelope"
	"loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	httpgen "loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/oauthgoogle"
	"loomtale/api/internal/secrets"
	"loomtale/api/internal/tenant"
	"loomtale/api/internal/youtube"
)

// fakeGoogleForChannels doubles Google's token and revoke endpoints and the
// Data API channels.list, for the in-process channels handlers.
type fakeGoogleForChannels struct {
	srv *httptest.Server

	mu         sync.Mutex
	challenges map[string]string // code -> PKCE challenge it was issued for
	scope      string
	revoked    []string
	noChannel  bool
}

func newFakeGoogleForChannels(t *testing.T) *fakeGoogleForChannels {
	f := &fakeGoogleForChannels{
		challenges: map[string]string{},
		scope:      strings.Join(oauthgoogle.Scopes, " "),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		code := r.PostForm.Get("code")
		want, ok := f.challenges[code]
		delete(f.challenges, code)
		verifier := r.PostForm.Get("code_verifier")
		if !ok || pkceChallenge(verifier) != want {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-1","expires_in":3599,"refresh_token":"refresh-secret-1","scope":"` + f.scope + `"}`))
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
	})
	mux.HandleFunc("GET /youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":401,"message":"bad token","errors":[{"reason":"authError"}]}}`))
			return
		}
		f.mu.Lock()
		empty := f.noChannel
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if empty {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"UCitchannel","snippet":{"title":"Night Tales","thumbnails":{"default":{"url":"https://yt3.example/d.jpg"}}},"status":{"longUploadsStatus":"eligible"},"contentDetails":{"relatedPlaylists":{"uploads":"UUitchannel"}}}]}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// pkceChallenge recomputes the S256 challenge the way Google does.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// consent simulates the user approving on Google's page: it issues a code
// for the challenge carried by the authorization URL.
func (f *fakeGoogleForChannels) consent(t *testing.T, authURL string) (state, code string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("access_type") != "offline" || q.Get("code_challenge_method") != "S256" || q.Get("prompt") != "consent" {
		t.Fatalf("authorization URL lacks offline PKCE consent: %s", authURL)
	}
	code = "code-" + uuid.NewString()
	f.mu.Lock()
	f.challenges[code] = q.Get("code_challenge")
	f.mu.Unlock()
	return q.Get("state"), code
}

func (f *fakeGoogleForChannels) revokedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked...)
}

type channelsFixture struct {
	api    *channelsapi.ChannelsAPI
	google *fakeGoogleForChannels
	owner  *pgxpool.Pool
	user   fixtureUser
	ctx    context.Context // session A of user in its tenant
	ctxB   context.Context // session B of the same user
}

func newChannelsFixture(t *testing.T) *channelsFixture {
	t.Helper()
	owner := ownerPool(t)
	app := appPool(t)
	oq := gen.New(owner)
	user := createFixtureUser(t, oq, "yt-"+uuid.NewString()[:8], uniqueEmail("yt"), "owner")

	sealer, err := envelope.NewSealer("it-test", [32]byte{1, 2, 3, 4, 5, 6, 7, 8})
	if err != nil {
		t.Fatal(err)
	}
	google := newFakeGoogleForChannels(t)
	aq := gen.New(app)
	project := "it-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DELETE FROM quota_ledger WHERE project = $1", project)
	})
	api := &channelsapi.ChannelsAPI{
		Queries: aq,
		Secrets: &secrets.Store{Sealer: sealer, Queries: aq},
		OAuth: &oauthgoogle.Config{
			ClientID: "cid", ClientSecret: "csecret",
			RedirectURL: "http://127.0.0.1:8080/api/v1/channels/oauth/callback",
			HTTP:        google.srv.Client(),
			AuthURL:     "https://accounts.example/o/oauth2/v2/auth",
			TokenURL:    google.srv.URL + "/token",
			RevokeURL:   google.srv.URL + "/revoke",
		},
		Ledger: &youtube.Ledger{Store: aq, Config: youtube.QuotaConfig{
			Project: project, DailyLimit: 10000, InsertCost: 1600, WriteCost: 50, ReadCost: 1,
		}},
		YouTubeAPIBase: google.srv.URL + "/youtube/v3",
	}
	return &channelsFixture{
		api: api, google: google, owner: owner, user: user,
		ctx:  sessionCtx(t, oq, user),
		ctxB: sessionCtx(t, oq, user),
	}
}

// sessionCtx creates a real session row and returns a request context
// carrying it and the user's tenant, as the auth middleware would.
func sessionCtx(t *testing.T, q *gen.Queries, u fixtureUser) context.Context {
	t.Helper()
	sessID := idconv.NewV7()
	tid := u.TenantID
	if _, err := q.CreateSession(context.Background(), gen.CreateSessionParams{
		ID:             idconv.ToPg(sessID),
		UserID:         idconv.ToPg(u.UserID),
		ActiveTenantID: idconv.ToPg(tid),
		TokenHash:      []byte(uuid.NewString()),
		ExpiresAt:      idconv.ToPgTimestamptz(time.Now().Add(time.Hour)),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	ctx := authpkg.WithSession(context.Background(), authpkg.Session{ID: sessID, UserID: u.UserID, ActiveTenantID: &tid, ExpiresAt: time.Now().Add(time.Hour)})
	return tenant.WithInfo(ctx, tenant.Info{ID: tid, Role: "owner"})
}

func (fx *channelsFixture) start(t *testing.T, ctx context.Context) string {
	t.Helper()
	resp, err := fx.api.StartYouTubeConnect(ctx, httpgen.StartYouTubeConnectRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	ok, isOK := resp.(httpgen.StartYouTubeConnect200JSONResponse)
	if !isOK {
		t.Fatalf("start connect: %#v", resp)
	}
	return ok.AuthorizationUrl
}

func (fx *channelsFixture) callback(t *testing.T, ctx context.Context, state, code, googleErr string) url.Values {
	t.Helper()
	p := httpgen.YouTubeOAuthCallbackParams{}
	if state != "" {
		p.State = &state
	}
	if code != "" {
		p.Code = &code
	}
	if googleErr != "" {
		p.Error = &googleErr
	}
	resp, err := fx.api.YouTubeOAuthCallback(ctx, httpgen.YouTubeOAuthCallbackRequestObject{Params: p})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := resp.(httpgen.YouTubeOAuthCallback302Response)
	if !ok {
		t.Fatalf("callback: %#v", resp)
	}
	loc, err := url.Parse(r.Headers.Location)
	if err != nil || loc.Path != "/settings/youtube" || loc.Host != "" {
		t.Fatalf("callback must redirect to the same-origin settings page, got %q", r.Headers.Location)
	}
	return loc.Query()
}

func requireConnectError(t *testing.T, q url.Values, reason string) {
	t.Helper()
	if q.Get("connect") != "error" || q.Get("reason") != reason {
		t.Fatalf("got %v, want connect=error reason=%s", q, reason)
	}
}

func (fx *channelsFixture) list(t *testing.T, ctx context.Context) httpgen.YouTubeChannelList {
	t.Helper()
	resp, err := fx.api.ListYouTubeChannels(ctx, httpgen.ListYouTubeChannelsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	return httpgen.YouTubeChannelList(resp.(httpgen.ListYouTubeChannels200JSONResponse))
}

func (fx *channelsFixture) auditCount(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := fx.owner.QueryRow(context.Background(),
		"SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = $2", fx.user.TenantID, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestYouTubeOAuthStateIsSingleUseAndSessionBound covers the handshake
// guards: another session cannot use a state, a state cannot be replayed,
// an expired state is refused, and a denied consent is reported.
func TestYouTubeOAuthStateIsSingleUseAndSessionBound(t *testing.T) {
	fx := newChannelsFixture(t)

	state, code := fx.google.consent(t, fx.start(t, fx.ctx))
	requireConnectError(t, fx.callback(t, fx.ctxB, state, code, ""), "invalid_state")
	// The attempt from the other session consumed it: the owner cannot
	// replay it either.
	requireConnectError(t, fx.callback(t, fx.ctx, state, code, ""), "invalid_state")

	state, code = fx.google.consent(t, fx.start(t, fx.ctx))
	if _, err := fx.owner.Exec(context.Background(),
		"UPDATE youtube_oauth_states SET expires_at = now() - interval '1 second' WHERE state_hash = $1",
		oauthgoogle.StateHash(state)); err != nil {
		t.Fatal(err)
	}
	requireConnectError(t, fx.callback(t, fx.ctx, state, code, ""), "invalid_state")

	state, _ = fx.google.consent(t, fx.start(t, fx.ctx))
	requireConnectError(t, fx.callback(t, fx.ctx, state, "", "access_denied"), "consent_denied")
	requireConnectError(t, fx.callback(t, fx.ctx, "", "", ""), "invalid_request")

	if got := fx.list(t, fx.ctx).Items; len(got) != 0 {
		t.Fatalf("no channel may be stored by a failed handshake, got %d", len(got))
	}
}

// TestYouTubeChannelConnectListAuditDisconnect runs the whole lifecycle
// against the Google double and checks the token is sealed at rest,
// revoked on disconnect and never returned by the API.
func TestYouTubeChannelConnectListAuditDisconnect(t *testing.T) {
	fx := newChannelsFixture(t)

	state, code := fx.google.consent(t, fx.start(t, fx.ctx))
	q := fx.callback(t, fx.ctx, state, code, "")
	if q.Get("connect") != "ok" || q.Get("channel") == "" {
		t.Fatalf("connect: %v", q)
	}

	list := fx.list(t, fx.ctx)
	if !list.OauthConfigured || len(list.Items) != 1 {
		t.Fatalf("list after connect: %+v", list)
	}
	ch := list.Items[0]
	if ch.YoutubeChannelId != "UCitchannel" || ch.Title != "Night Tales" || !ch.CanUpload ||
		ch.Status != "connected" || ch.LongUploadsStatus != "eligible" || ch.ApiProjectAudited ||
		ch.EligibilityCheckedAt == nil || ch.Id.String() != q.Get("channel") {
		t.Fatalf("channel: %+v", ch)
	}
	if list.Quota.Used != 1 || list.Quota.Limit != 10000 || !list.Quota.ResetsAt.After(time.Now()) {
		t.Errorf("channels.list must be charged to the ledger: %+v", list.Quota)
	}

	// Sealed at rest: the stored row never contains the token bytes, and
	// only the secrets store can open it.
	var ciphertext []byte
	if err := fx.owner.QueryRow(context.Background(),
		"SELECT ciphertext FROM secrets WHERE tenant_id = $1 AND kind = 'youtube_refresh' AND owner_ref = $2",
		fx.user.TenantID, ch.Id.String()).Scan(&ciphertext); err != nil {
		t.Fatalf("refresh token not stored: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("refresh-secret-1")) {
		t.Fatal("refresh token stored in plaintext")
	}
	if got, err := fx.api.Secrets.Open(fx.ctx, fx.user.TenantID, secrets.KindYouTubeRefresh, ch.Id.String()); err != nil || got != "refresh-secret-1" {
		t.Fatalf("open sealed token: %q %v", got, err)
	}
	if fx.auditCount(t, "youtube_channel.connect") != 1 {
		t.Error("connect must be audited")
	}

	// Reconnecting the same channel keeps its id.
	state, code = fx.google.consent(t, fx.start(t, fx.ctx))
	if q := fx.callback(t, fx.ctx, state, code, ""); q.Get("channel") != ch.Id.String() {
		t.Fatalf("reconnect must keep the channel id: %v", q)
	}

	// Audit toggle.
	note := "form sent"
	day := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	resp, err := fx.api.UpdateYouTubeChannelAudit(fx.ctx, httpgen.UpdateYouTubeChannelAuditRequestObject{
		Id: ch.Id, Body: &httpgen.UpdateYouTubeChannelAuditJSONRequestBody{
			ApiProjectAudited: true, AuditNote: &note, AuditFormDate: &openapi_types.Date{Time: day},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	upd, ok := resp.(httpgen.UpdateYouTubeChannelAudit200JSONResponse)
	if !ok || !upd.ApiProjectAudited || upd.AuditNote != note || upd.AuditFormDate == nil || !upd.AuditFormDate.Equal(day) {
		t.Fatalf("audit update: %#v", resp)
	}
	if resp, _ := fx.api.UpdateYouTubeChannelAudit(fx.ctx, httpgen.UpdateYouTubeChannelAuditRequestObject{
		Id: uuid.New(), Body: &httpgen.UpdateYouTubeChannelAuditJSONRequestBody{},
	}); !isType[httpgen.UpdateYouTubeChannelAudit404ApplicationProblemPlusJSONResponse](resp) {
		t.Fatalf("unknown channel: %#v", resp)
	}

	// Another tenant sees nothing and cannot disconnect it.
	other := createFixtureUser(t, gen.New(fx.owner), "yt-other-"+uuid.NewString()[:8], uniqueEmail("yt-other"), "owner")
	otherCtx := sessionCtx(t, gen.New(fx.owner), other)
	if n := len(fx.list(t, otherCtx).Items); n != 0 {
		t.Fatalf("other tenant lists %d channels", n)
	}
	if resp, _ := fx.api.DisconnectYouTubeChannel(otherCtx, httpgen.DisconnectYouTubeChannelRequestObject{Id: ch.Id}); !isType[httpgen.DisconnectYouTubeChannel404ApplicationProblemPlusJSONResponse](resp) {
		t.Fatalf("cross-tenant disconnect: %#v", resp)
	}

	// Disconnect: revoked at Google, secret deleted, row kept.
	resp2, err := fx.api.DisconnectYouTubeChannel(fx.ctx, httpgen.DisconnectYouTubeChannelRequestObject{Id: ch.Id})
	if err != nil || !isType[httpgen.DisconnectYouTubeChannel204Response](resp2) {
		t.Fatalf("disconnect: %#v %v", resp2, err)
	}
	if rv := fx.google.revokedTokens(); len(rv) != 1 || rv[0] != "refresh-secret-1" {
		t.Errorf("revoked at google: %v", rv)
	}
	if _, err := fx.api.Secrets.Open(fx.ctx, fx.user.TenantID, secrets.KindYouTubeRefresh, ch.Id.String()); err == nil {
		t.Error("the refresh token must be deleted on disconnect")
	}
	after := fx.list(t, fx.ctx).Items
	if len(after) != 1 || after[0].Status != "disconnected" || after[0].CanUpload {
		t.Fatalf("after disconnect: %+v", after)
	}
	if fx.auditCount(t, "youtube_channel.disconnect") != 1 {
		t.Error("disconnect must be audited")
	}
	if resp, err := fx.api.DisconnectYouTubeChannel(fx.ctx, httpgen.DisconnectYouTubeChannelRequestObject{Id: ch.Id}); err != nil || !isType[httpgen.DisconnectYouTubeChannel204Response](resp) {
		t.Fatalf("disconnect is idempotent: %#v %v", resp, err)
	}
}

// TestYouTubeConnectRefusesUnusableGrants revokes grants that cannot be
// used: no upload scope, or a Google account without a channel.
func TestYouTubeConnectRefusesUnusableGrants(t *testing.T) {
	fx := newChannelsFixture(t)

	fx.google.mu.Lock()
	fx.google.scope = "https://www.googleapis.com/auth/youtube.readonly"
	fx.google.mu.Unlock()
	state, code := fx.google.consent(t, fx.start(t, fx.ctx))
	requireConnectError(t, fx.callback(t, fx.ctx, state, code, ""), "upload_scope_missing")

	fx.google.mu.Lock()
	fx.google.scope = strings.Join(oauthgoogle.Scopes, " ")
	fx.google.noChannel = true
	fx.google.mu.Unlock()
	state, code = fx.google.consent(t, fx.start(t, fx.ctx))
	requireConnectError(t, fx.callback(t, fx.ctx, state, code, ""), "no_channel")

	if rv := fx.google.revokedTokens(); len(rv) != 2 {
		t.Errorf("both unusable grants must be revoked, got %v", rv)
	}
	if n := len(fx.list(t, fx.ctx).Items); n != 0 {
		t.Fatalf("no channel may be stored, got %d", n)
	}
}

// TestYouTubeConnectUnconfigured: without a Google client the page says
// so and connecting answers 503 instead of a broken consent URL.
func TestYouTubeConnectUnconfigured(t *testing.T) {
	fx := newChannelsFixture(t)
	fx.api.OAuth = &oauthgoogle.Config{}
	if fx.list(t, fx.ctx).OauthConfigured {
		t.Fatal("oauthConfigured must be false")
	}
	resp, err := fx.api.StartYouTubeConnect(fx.ctx, httpgen.StartYouTubeConnectRequestObject{})
	if err != nil || !isType[httpgen.StartYouTubeConnect503ApplicationProblemPlusJSONResponse](resp) {
		t.Fatalf("unconfigured connect: %#v %v", resp, err)
	}
}

// TestQuotaLedgerNeverOverspends reserves concurrently against the real
// table: exactly as many calls as fit in the daily limit succeed.
func TestQuotaLedgerNeverOverspends(t *testing.T) {
	owner := ownerPool(t)
	q := gen.New(appPool(t))
	project := "it-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "DELETE FROM quota_ledger WHERE project = $1", project)
	})
	l := &youtube.Ledger{Store: q, Config: youtube.QuotaConfig{Project: project, DailyLimit: 5000, InsertCost: 1600, WriteCost: 50, ReadCost: 1}}

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, refused := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := l.Reserve(context.Background(), youtube.OpVideosInsert)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case youtube.IsKind(err, youtube.KindQuota):
				refused++
			default:
				t.Errorf("reserve: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 3 || refused != 5 {
		t.Fatalf("3 inserts of 1600 fit in 5000: ok=%d refused=%d", ok, refused)
	}
	u, err := l.Usage(context.Background())
	if err != nil || u.Used != 4800 {
		t.Fatalf("usage = %+v %v, want 4800", u, err)
	}
	if err := l.Reserve(context.Background(), youtube.OpChannelsList); err != nil {
		t.Fatalf("a 1-unit read still fits: %v", err)
	}
	if err := l.MarkExhausted(context.Background(), youtube.OpVideosList); !youtube.IsKind(err, youtube.KindQuota) {
		t.Fatalf("mark exhausted: %v", err)
	}
	if err := l.Reserve(context.Background(), youtube.OpChannelsList); !youtube.IsKind(err, youtube.KindQuota) {
		t.Fatalf("after Google said quotaExceeded every call is refused locally: %v", err)
	}
}

func isType[T any](v any) bool {
	_, ok := v.(T)
	return ok
}
