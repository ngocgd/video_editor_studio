package youtube

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Kind classifies a failed Google call by what the caller should do next.
type Kind string

const (
	// KindQuota: the daily pool is spent; snooze until the Pacific reset.
	KindQuota Kind = "quota"
	// KindAuth: the token was rejected; the channel needs reconnecting.
	KindAuth Kind = "auth"
	// KindTransient: retry later (5xx, 429, per-user rate limits, network).
	KindTransient Kind = "transient"
	// KindPermanent: retrying cannot help; surface the reason verbatim.
	KindPermanent Kind = "permanent"
)

// Reasons the client itself assigns, stable for the UI and step results.
const (
	ReasonLongUploadsNotAllowed = "long_uploads_not_allowed"
	ReasonThumbnailNotPermitted = "thumbnail_not_permitted"
	ReasonRenderChanged         = "render_changed_since_approval"
	ReasonUploadSessionGone     = "upload_session_gone"
)

// APIError is every non-success answer from Google, classified.
type APIError struct {
	Kind   Kind
	Status int
	// Reason is Google's first error reason (e.g. "quotaExceeded",
	// "uploadLimitExceeded") or one of the Reason* constants above.
	Reason  string
	Message string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("youtube: %s: %s", e.Reason, e.Message)
	}
	return fmt.Sprintf("youtube: %d %s: %s", e.Status, e.Reason, e.Message)
}

// IsKind reports whether err is an *APIError of kind k. A
// *QuotaExceededError from the local ledger counts as KindQuota.
func IsKind(err error, k Kind) bool {
	var qe *QuotaExceededError
	if k == KindQuota && errors.As(err, &qe) {
		return true
	}
	var ae *APIError
	return errors.As(err, &ae) && ae.Kind == k
}

// HasReason reports whether err is an *APIError with the given reason.
func HasReason(err error, reason string) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Reason == reason
}

// googleErrorBody is the Data API's error envelope.
type googleErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Errors  []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	} `json:"error"`
}

// quotaReasons are the Data API reasons that mean the project's daily
// pool is spent (not a short per-user rate limit).
var quotaReasons = map[string]bool{"quotaExceeded": true, "dailyLimitExceeded": true}

// transientReasons are 403 reasons that clear on their own.
var transientReasons = map[string]bool{"rateLimitExceeded": true, "userRateLimitExceeded": true, "backendError": true}

// classify builds the APIError for a non-2xx response body. The message
// is Google's own, so a policy rejection reaches the UI verbatim.
func classify(status int, body []byte) *APIError {
	var g googleErrorBody
	_ = json.Unmarshal(body, &g)
	e := &APIError{Status: status, Message: g.Error.Message}
	if len(g.Error.Errors) > 0 {
		e.Reason = g.Error.Errors[0].Reason
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	switch {
	case quotaReasons[e.Reason]:
		e.Kind = KindQuota
	case status == http.StatusUnauthorized:
		e.Kind = KindAuth
	case status == http.StatusTooManyRequests || status >= 500 || transientReasons[e.Reason]:
		e.Kind = KindTransient
	default:
		e.Kind = KindPermanent
	}
	return e
}
