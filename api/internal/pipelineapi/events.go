package pipelineapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	authpkg "loomtale/api/internal/auth"
	"loomtale/api/internal/httpapi/gen"
	"loomtale/api/internal/sse"
	"loomtale/api/internal/tenant"
)

// StreamEvents implements gen.StrictServerInterface. It authorises every
// requested topic (a pipeline run id) against the caller's tenant, then
// hands the connection to sse.Hub for the life of the stream: the
// returned response's Body is a pipe fed by Subscriber.WriteTo, which
// only stops (closing the pipe, ending this request) when ctx is
// cancelled, i.e. the client disconnects.
func (h *PipelineAPI) StreamEvents(ctx context.Context, req gen.StreamEventsRequestObject) (gen.StreamEventsResponseObject, error) {
	sess, ok := authpkg.FromCtx(ctx)
	if !ok {
		detail := "authentication required"
		return gen.StreamEvents403ApplicationProblemPlusJSONResponse{Title: "unauthorised", Status: http.StatusForbidden, Detail: &detail}, nil
	}
	info := tenant.MustFromCtx(ctx)

	topics, err := h.authorizedTopics(ctx, info.ID, req.Params.Topics)
	if err != nil {
		detail := err.Error()
		return gen.StreamEvents403ApplicationProblemPlusJSONResponse{Title: "unknown or unauthorised topic", Status: http.StatusForbidden, Detail: &detail}, nil
	}

	sub, err := h.Hub.Subscribe(ctx, sess.UserID, info.ID, topics)
	if errors.Is(err, sse.ErrTooManyStreams) {
		detail := err.Error()
		return gen.StreamEvents429ApplicationProblemPlusJSONResponse{Title: "too many open streams", Status: http.StatusTooManyRequests, Detail: &detail}, nil
	}
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	go func() {
		_, err := sub.WriteTo(pw)
		_ = pw.CloseWithError(err)
	}()

	return gen.StreamEvents200TexteventStreamResponse{Body: pr}, nil
}

func (h *PipelineAPI) authorizedTopics(ctx context.Context, tenantID uuid.UUID, raw string) (map[string]bool, error) {
	topics := make(map[string]bool)
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		runID, err := uuid.Parse(t)
		if err != nil {
			return nil, errBadTopic(t)
		}
		ok, err := h.RunBelongsToTenant(ctx, tenantID, runID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errBadTopic(t)
		}
		topics[t] = true
	}
	if len(topics) == 0 {
		return nil, errBadTopic(raw)
	}
	return topics, nil
}

func errBadTopic(t string) error {
	return &badTopicError{topic: t}
}

type badTopicError struct{ topic string }

func (e *badTopicError) Error() string { return "topic not visible to this tenant: " + e.topic }
