// Package analyticsapi serves /analytics: the channel overview, the
// tracked-video table and detail, tracking an existing video, "Sync now",
// suggestions and the LLM Explain step. Every read comes from the
// pre-aggregated tables of package analytics and is tenant scoped; the
// channel (or tracked video) is checked to belong to the caller's tenant
// before anything else.
package analyticsapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"loomtale/api/internal/analytics"
	authpkg "loomtale/api/internal/auth"
	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/pipeline"
)

// DefaultWindowDays is the window used when a request names none.
const DefaultWindowDays = 28

// MaxWindowDays bounds a requested window.
const MaxWindowDays = 366

// JobInserter enqueues River jobs (the api's insert-only River client).
type JobInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Enqueuer starts pipeline runs (the Explain step).
type Enqueuer interface {
	Enqueue(ctx context.Context, tenantID uuid.UUID, spec pipeline.RunSpec) (uuid.UUID, error)
}

// AnalyticsAPI implements the /analytics operations of
// gen.StrictServerInterface.
type AnalyticsAPI struct {
	Queries    *dbgen.Queries
	Aggregator *analytics.Aggregator
	// Clients builds a channel's Google clients (tracking a video looks it
	// up with the Data API).
	Clients analytics.ClientFactory
	Jobs    JobInserter
	Engine  Enqueuer
}

// errBadWindow is a from/to pair the API refuses.
var errBadWindow = errors.New("the window must have from <= to and span at most 366 days")

// channel reads a channel of the caller's tenant; ok is false when it
// does not exist there.
func (h *AnalyticsAPI) channel(ctx context.Context, tenantID, channelID uuid.UUID) (dbgen.YoutubeChannel, bool, error) {
	row, err := h.Queries.GetYouTubeChannel(ctx, dbgen.GetYouTubeChannelParams{TenantID: idconv.ToPg(tenantID), ID: idconv.ToPg(channelID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, fmt.Errorf("analyticsapi: read channel: %w", err)
	}
	return row, true, nil
}

// window resolves the requested window: to defaults to the newest synced
// day (or the last complete Pacific day), from to DefaultWindowDays
// before to.
func (h *AnalyticsAPI) window(st analytics.SyncStatus, from, to *openapi_types.Date) (analytics.Window, error) {
	w := h.Aggregator.DefaultWindow(st, DefaultWindowDays)
	if to != nil {
		w.To = to.Time
		w.From = w.To.AddDate(0, 0, -(DefaultWindowDays - 1))
	}
	if from != nil {
		w.From = from.Time
	}
	if w.From.After(w.To) || w.Days() > MaxWindowDays {
		return w, errBadWindow
	}
	return w, nil
}

// sessionUser is the signed-in user, if any.
func sessionUser(ctx context.Context) *uuid.UUID {
	if sess, ok := authpkg.FromCtx(ctx); ok && sess.UserID != uuid.Nil {
		id := sess.UserID
		return &id
	}
	return nil
}

func problem(status int, title, detail string) gen.Problem {
	return gen.Problem{Title: title, Status: status, Detail: &detail}
}

func channelNotFound() gen.AnalyticsChannelNotFoundApplicationProblemPlusJSONResponse {
	return gen.AnalyticsChannelNotFoundApplicationProblemPlusJSONResponse(problem(404, "channel not found", "no such channel in this workspace"))
}

func videoNotFound() gen.AnalyticsVideoNotFoundApplicationProblemPlusJSONResponse {
	return gen.AnalyticsVideoNotFoundApplicationProblemPlusJSONResponse(problem(404, "video not tracked", "this video is not tracked in this workspace"))
}

func badRequest(detail string) gen.AnalyticsBadRequestApplicationProblemPlusJSONResponse {
	return gen.AnalyticsBadRequestApplicationProblemPlusJSONResponse(problem(400, "invalid request", detail))
}

func date(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t} }

func datePtr(t *time.Time) *openapi_types.Date {
	if t == nil {
		return nil
	}
	d := date(*t)
	return &d
}

func toWindow(w analytics.Window) gen.AnalyticsWindow {
	return gen.AnalyticsWindow{From: date(w.From), To: date(w.To)}
}

func toSyncState(st analytics.SyncStatus) gen.AnalyticsSyncState {
	return gen.AnalyticsSyncState{
		Status:           gen.AnalyticsSyncStateStatus(st.Status),
		AnalyticsThrough: datePtr(st.AnalyticsThrough),
		ReachThrough:     datePtr(st.ReachThrough),
		LastStartedAt:    st.LastStartedAt,
		LastFinishedAt:   st.LastFinishedAt,
		LastError:        st.LastError,
		SubscriberCount:  st.SubscriberCount,
	}
}

func unavailable(m map[string]string) gen.Unavailable {
	if m == nil {
		return gen.Unavailable{}
	}
	return gen.Unavailable(m)
}

func intPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}
