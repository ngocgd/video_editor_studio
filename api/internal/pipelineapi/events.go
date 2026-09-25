package pipelineapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/sse"
	"loomtale/api/internal/tenant"
)

// maxTopicsPerStream bounds how many run ids one /events connection can
// subscribe to: unbounded topics would mean unbounded query size (a
// crafted request could fit thousands of ids in the URL) and an
// unbounded per-connection authorization check on every subscribe.
const maxTopicsPerStream = 50

// StreamEvents implements gen.StrictServerInterface. It authorises every
// requested topic (a pipeline run id) against the caller's tenant in one
// batch query, then hands the connection to sse.Hub for the life of the
// stream: the returned response's Body is a pipe fed by
// Subscriber.WriteTo, which stops (closing the pipe, ending this
// request) when either the request's own context ends (client
// disconnect) or h.ShutdownSignal does (graceful process shutdown).
func (h *PipelineAPI) StreamEvents(ctx context.Context, req gen.StreamEventsRequestObject) (gen.StreamEventsResponseObject, error) {
	sess, ok := authpkg.FromCtx(ctx)
	if !ok {
		detail := "authentication required"
		return gen.StreamEvents403ApplicationProblemPlusJSONResponse{Title: "unauthorised", Status: http.StatusForbidden, Detail: &detail}, nil
	}
	info := tenant.MustFromCtx(ctx)

	topics, err := h.authorizedTopics(ctx, info.ID, req.Params.Topics)
	if err != nil {
		if errors.Is(err, errBadTopic) {
			detail := err.Error()
			return gen.StreamEvents403ApplicationProblemPlusJSONResponse{Title: "unknown or unauthorised topic", Status: http.StatusForbidden, Detail: &detail}, nil
		}
		// A genuine backend failure (the database is down) must never be
		// reported as "topic unauthorised": that would be misleading to
		// the caller and, worse, would echo the underlying error's own
		// text into a client-facing 403 detail.
		return nil, err
	}

	subCtx, cancelSub := context.WithCancel(ctx)
	stopWatchingShutdown := func() bool { return false }
	if h.ShutdownSignal != nil {
		stopWatchingShutdown = context.AfterFunc(h.ShutdownSignal, cancelSub)
	}

	sub, err := h.Hub.Subscribe(subCtx, sess.UserID, info.ID, topics)
	if errors.Is(err, sse.ErrTooManyStreams) {
		cancelSub()
		stopWatchingShutdown()
		detail := err.Error()
		return gen.StreamEvents429ApplicationProblemPlusJSONResponse{Title: "too many open streams", Status: http.StatusTooManyRequests, Detail: &detail}, nil
	}
	if err != nil {
		cancelSub()
		stopWatchingShutdown()
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		defer cancelSub()
		defer stopWatchingShutdown()
		_, err := sub.WriteTo(pw)
		_ = pw.CloseWithError(err)
	}()

	return gen.StreamEvents200TexteventStreamResponse{Body: pr}, nil
}

// authorizedTopics parses and authorizes the topics query parameter in
// one batch query against the database instead of one round trip per
// topic.
func (h *PipelineAPI) authorizedTopics(ctx context.Context, tenantID uuid.UUID, raw string) (map[string]bool, error) {
	var runIDs []uuid.UUID
	var rawTopics []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		runID, err := uuid.Parse(t)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", errBadTopic, t)
		}
		rawTopics = append(rawTopics, t)
		runIDs = append(runIDs, runID)
	}
	if len(rawTopics) == 0 {
		return nil, fmt.Errorf("%w: no topics requested", errBadTopic)
	}
	if len(rawTopics) > maxTopicsPerStream {
		return nil, fmt.Errorf("%w: at most %d topics per stream", errBadTopic, maxTopicsPerStream)
	}

	owned, err := h.Engine.RunsBelongToTenant(ctx, tenantID, runIDs)
	if err != nil {
		return nil, err
	}
	topics := make(map[string]bool, len(rawTopics))
	for i, t := range rawTopics {
		if !owned[runIDs[i]] {
			return nil, fmt.Errorf("%w: %q", errBadTopic, t)
		}
		topics[t] = true
	}
	return topics, nil
}

// errBadTopic is a sentinel (not a formatted error itself) so
// StreamEvents can distinguish "the caller asked for a bad topic" (403)
// from any other error authorizedTopics might propagate (500): wrap it
// with fmt.Errorf("%w: ...", errBadTopic) for a specific message while
// keeping errors.Is(err, errBadTopic) true.
var errBadTopic = errors.New("topic not visible to this tenant")
