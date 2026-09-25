package authapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"loomtale/api/internal/audit"
	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/csrf"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/httpx"
)

// dummyHash is verified against on a login for an unknown email, so a
// failed lookup costs the same argon2id time as a failed password check
// and does not leak which emails are registered via a timing side channel.
var dummyHash, _ = authpkg.HashPassword("loomtale-dummy-password-for-constant-time-lookup")

// withCookie wraps a gen.LoginResponseObject to also set the session
// cookie: the generated Visit method only serializes the JSON body, so a
// header/cookie needs a small wrapper like this rather than editing
// generated code.
type withCookie struct {
	gen.LoginResponseObject
	token     string
	expiresAt time.Time
}

func (w withCookie) VisitLoginResponse(rw http.ResponseWriter) error {
	authpkg.SetCookie(rw, w.token, w.expiresAt)
	return w.LoginResponseObject.VisitLoginResponse(rw)
}

// Login implements gen.StrictServerInterface.
func (h *AuthAPI) Login(ctx context.Context, req gen.LoginRequestObject) (gen.LoginResponseObject, error) {
	r := httpx.RequestFromCtx(ctx)
	ip := httpx.ClientIP(r)
	email := strings.ToLower(string(req.Body.Email))

	allowed, err := h.LoginPerIP.Allow(ctx, "login:ip:"+ip)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return rateLimited("too many login attempts from this address"), nil
	}
	allowed, err = h.LoginPerUser.Allow(ctx, "login:user:"+ip+":"+email)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return rateLimited("too many login attempts for this account"), nil
	}
	allowed, err = h.LoginPerAccount.Allow(ctx, "login:account:"+email)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return rateLimited("too many login attempts for this account"), nil
	}

	if !h.HashLimiter.TryAcquire() {
		detail := "too many concurrent login attempts; try again shortly"
		return gen.Login429ApplicationProblemPlusJSONResponse{Title: "server busy", Status: http.StatusTooManyRequests, Detail: &detail}, nil
	}
	defer h.HashLimiter.Release()

	user, err := h.Queries.GetUserByEmail(ctx, email)
	if err != nil {
		_, _, _ = authpkg.VerifyPassword(dummyHash, req.Body.Password) // constant-time decoy
		h.auditFailedLogin(ctx, ip, r.UserAgent(), email)
		return unauthorized(), nil
	}

	ok, needsRehash, err := authpkg.VerifyPassword(user.PasswordHash, req.Body.Password)
	if err != nil || !ok {
		h.auditFailedLogin(ctx, ip, r.UserAgent(), email)
		return unauthorized(), nil
	}

	userID := idconv.FromPg(user.ID)
	if needsRehash {
		if newHash, herr := authpkg.HashPassword(req.Body.Password); herr == nil {
			_ = h.Queries.UpdateUserPasswordHash(ctx, dbgen.UpdateUserPasswordHashParams{ID: user.ID, PasswordHash: newHash})
		}
	}

	memberships, err := h.Queries.ListMembershipsForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	var activeTenantID *uuid.UUID
	if len(memberships) > 0 {
		id := idconv.FromPg(memberships[0].TenantID)
		activeTenantID = &id
	}

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := h.Queries.WithTx(tx)

	created, err := h.Store.Create(ctx, qtx, userID, activeTenantID)
	if err != nil {
		return nil, err
	}

	if err := audit.Record(ctx, qtx, audit.Entry{
		TenantID:    activeTenantID,
		ActorUserID: &userID,
		ActorEmail:  email,
		Action:      "login_succeeded",
		RemoteAddr:  r.RemoteAddr,
		UserAgent:   r.UserAgent(),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	me := buildMe(userID, email, activeTenantID, memberships)
	body := gen.Login200JSONResponse{Me: me, CsrfToken: csrf.Derive(h.CSRFPepper, created.Token)}
	return withCookie{LoginResponseObject: body, token: created.Token, expiresAt: created.Session.ExpiresAt.Time}, nil
}

// looksLikeEmail is a loose sanity check, not a validator: it exists only
// to decide whether the attempted-login value is safe to record verbatim.
// A value with no "@" is very likely a password mistyped into the email
// field (or garbage), and recording it verbatim into an append-only audit
// log would permanently capture what may be a real credential.
var looksLikeEmail = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func (h *AuthAPI) auditFailedLogin(ctx context.Context, ip, ua, attemptedEmail string) {
	recorded := attemptedEmail
	if !looksLikeEmail.MatchString(attemptedEmail) {
		recorded = "[REDACTED: does not look like an email]"
	}
	_ = audit.Record(ctx, h.Queries, audit.Entry{
		Action:     "login_failed",
		Metadata:   map[string]any{"email": recorded},
		RemoteAddr: ip,
		UserAgent:  ua,
	})
}

func unauthorized() gen.LoginResponseObject {
	detail := "invalid email or password"
	return gen.Login401ApplicationProblemPlusJSONResponse{Title: "unauthorized", Status: http.StatusUnauthorized, Detail: &detail}
}

func rateLimited(detail string) gen.LoginResponseObject {
	return gen.Login429ApplicationProblemPlusJSONResponse{Title: "rate limited", Status: http.StatusTooManyRequests, Detail: &detail}
}
