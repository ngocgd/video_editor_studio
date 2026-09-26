package oauthgoogle

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeGoogle is an httptest double of Google's token and revoke endpoints.
type fakeGoogle struct {
	srv *httptest.Server

	mu        sync.Mutex
	challenge string // code_challenge the code was issued for
	code      string
	refresh   string
	revoked   []string
	refreshes int
	rotateTo  string // next refresh grant returns this new refresh token
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	f := &fakeGoogle{code: "auth-code", refresh: "refresh-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		tok := r.PostForm.Get("token")
		if tok != f.refresh {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
		f.revoked = append(f.revoked, tok)
		f.refresh = ""
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"` + code + `","error_description":"test"}`))
	}
	if r.PostForm.Get("client_id") != "cid" || r.PostForm.Get("client_secret") != "csecret" {
		fail("invalid_client")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.PostForm.Get("code") != f.code || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge ||
			r.PostForm.Get("redirect_uri") != "https://studio.example/api/v1/channels/oauth/callback" {
			fail("invalid_grant")
			return
		}
		f.code = "" // single use
		_, _ = w.Write([]byte(`{"access_token":"access-1","expires_in":3599,"refresh_token":"` + f.refresh +
			`","scope":"https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly","token_type":"Bearer"}`))
	case "refresh_token":
		if f.refresh == "" || r.PostForm.Get("refresh_token") != f.refresh {
			fail("invalid_grant")
			return
		}
		f.refreshes++
		extra := ""
		if f.rotateTo != "" {
			f.refresh, extra = f.rotateTo, `,"refresh_token":"`+f.rotateTo+`"`
			f.rotateTo = ""
		}
		_, _ = w.Write([]byte(`{"access_token":"access-r","expires_in":3599` + extra + `}`))
	default:
		fail("unsupported_grant_type")
	}
}

func (f *fakeGoogle) config() *Config {
	return &Config{
		ClientID: "cid", ClientSecret: "csecret",
		RedirectURL: "https://studio.example/api/v1/channels/oauth/callback",
		HTTP:        f.srv.Client(),
		AuthURL:     "https://accounts.example/auth",
		TokenURL:    f.srv.URL + "/token",
		RevokeURL:   f.srv.URL + "/revoke",
	}
}

func TestAuthCodeURLCarriesPKCEAndOfflineConsent(t *testing.T) {
	cfg := (&fakeGoogle{}).configFor()
	verifier, _ := NewVerifier()
	u, err := url.Parse(cfg.AuthCodeURL("st", verifier))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	sum := sha256.Sum256([]byte(verifier))
	want := map[string]string{
		"client_id": "cid", "redirect_uri": cfg.RedirectURL, "response_type": "code", "state": "st",
		"code_challenge": base64.RawURLEncoding.EncodeToString(sum[:]), "code_challenge_method": "S256",
		"access_type": "offline", "prompt": "consent", "scope": strings.Join(Scopes, " "),
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Get("client_secret") != "" {
		t.Error("the client secret must never reach the browser")
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Errorf("verifier length %d outside RFC 7636 bounds", len(verifier))
	}
}

// configFor builds a config without a server, for URL-only tests.
func (f *fakeGoogle) configFor() *Config {
	return &Config{ClientID: "cid", ClientSecret: "csecret", RedirectURL: "https://studio.example/cb", AuthURL: "https://accounts.example/auth"}
}

func TestExchangeRequiresMatchingVerifier(t *testing.T) {
	f := newFakeGoogle(t)
	cfg := f.config()
	verifier, _ := NewVerifier()
	f.mu.Lock()
	f.challenge = challenge(verifier)
	f.mu.Unlock()

	if _, err := cfg.Exchange(context.Background(), "auth-code", "wrong-verifier-wrong-verifier-wrong-verifier"); !IsReconnectNeeded(err) {
		t.Fatalf("wrong verifier: got %v, want invalid_grant", err)
	}
	tok, err := cfg.Exchange(context.Background(), "auth-code", verifier)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access-1" || tok.RefreshToken != "refresh-1" || !HasScope(tok.Scopes, UploadScope) {
		t.Fatalf("token = %+v", tok)
	}
	if _, err := cfg.Exchange(context.Background(), "auth-code", verifier); err == nil {
		t.Fatal("a code must not be exchangeable twice")
	}
}

func TestTokenSourceRefreshesRotatesAndDetectsDeadGrant(t *testing.T) {
	f := newFakeGoogle(t)
	var rotated string
	src := &TokenSource{Config: f.config(), RefreshToken: "refresh-1",
		OnRotate: func(_ context.Context, rt string) error { rotated = rt; return nil }}

	var authMu sync.Mutex
	var gotAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.Lock()
		gotAuth = r.Header.Get("Authorization")
		authMu.Unlock()
	}))
	defer api.Close()
	hc := Client(api.Client(), src)

	f.mu.Lock()
	f.rotateTo = "refresh-2"
	f.mu.Unlock()
	for i := 0; i < 2; i++ {
		resp, err := hc.Get(api.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	authMu.Lock()
	if gotAuth != "Bearer access-r" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	authMu.Unlock()
	f.mu.Lock()
	if f.refreshes != 1 {
		t.Errorf("refreshed %d times, want 1 (the access token is cached)", f.refreshes)
	}
	f.mu.Unlock()
	if rotated != "refresh-2" || src.RefreshToken != "refresh-2" {
		t.Errorf("rotation not persisted: callback %q, source %q", rotated, src.RefreshToken)
	}

	// Revoke kills the grant; a fresh source then reports reconnect needed.
	if err := f.config().Revoke(context.Background(), "refresh-2"); err != nil {
		t.Fatal(err)
	}
	if err := f.config().Revoke(context.Background(), "refresh-2"); err != nil {
		t.Fatalf("revoking an already revoked token is success: %v", err)
	}
	dead := &TokenSource{Config: f.config(), RefreshToken: "refresh-2"}
	_, err := Client(api.Client(), dead).Get(api.URL)
	if !IsReconnectNeeded(err) {
		t.Fatalf("dead grant: got %v, want reconnect needed", err)
	}
}

func TestStateHashIsStable(t *testing.T) {
	s1, _ := NewState()
	s2, _ := NewState()
	if s1 == s2 || len(StateHash(s1)) != 32 || string(StateHash(s1)) != string(StateHash(s1)) {
		t.Fatal("states must be unique and hash to 32 stable bytes")
	}
}
