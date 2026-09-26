package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"
	"loomtale/api/internal/db/idconv"
)

// ErrSuggestionNotFound means no such suggestion exists for the channel.
var ErrSuggestionNotFound = errors.New("analytics: suggestion not found")

// RuleInput loads the rules' input for one channel: tracked video totals
// over the RuleWindowDays ending at the newest synced day, with each
// video's retention curve.
func (a *Aggregator) RuleInput(ctx context.Context, tenantID, channelID uuid.UUID) (RuleInput, error) {
	st, err := a.SyncStatus(ctx, tenantID, channelID)
	if err != nil {
		return RuleInput{}, err
	}
	w := a.DefaultWindow(st, RuleWindowDays)
	totals, err := a.VideoTotals(ctx, tenantID, channelID, w)
	if err != nil {
		return RuleInput{}, err
	}
	in := RuleInput{Now: a.now(), Window: w, Videos: make([]RuleVideo, 0, len(totals))}
	tenant := idconv.ToPg(tenantID)
	for _, t := range totals {
		v := RuleVideo{
			VideoID: t.VideoID, DurationSeconds: t.DurationSeconds, PublishedAt: t.PublishedAt,
			Views: t.Views, Impressions: t.Impressions, CTR: t.CTR, AverageViewPercentage: t.AverageViewPercentage,
		}
		curve, err := a.Queries.VideoRetentionCurve(ctx, dbgen.VideoRetentionCurveParams{TenantID: tenant, YoutubeVideoID: t.VideoID})
		if err != nil {
			return RuleInput{}, fmt.Errorf("analytics: retention: %w", err)
		}
		for _, c := range curve {
			if c.AudienceWatchRatio.Valid {
				v.Retention = append(v.Retention, RetentionPoint{Ratio: c.ElapsedRatio, AudienceWatchRatio: c.AudienceWatchRatio.Float64})
			}
		}
		in.Videos = append(in.Videos, v)
	}
	return in, nil
}

// RecomputeSuggestions evaluates the rules for a channel and replaces its
// suggestions in one transaction: fired rules are upserted (a dismissal
// survives unless the rule version changed), the rest are deleted.
func (a *Aggregator) RecomputeSuggestions(ctx context.Context, tenantID, channelID uuid.UUID) (int, error) {
	in, err := a.RuleInput(ctx, tenantID, channelID)
	if err != nil {
		return 0, err
	}
	findings := Evaluate(in)

	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("analytics: begin suggestions: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := a.Queries.WithTx(tx)
	tenant, channel := idconv.ToPg(tenantID), idconv.ToPg(channelID)
	keep := make([]string, 0, len(findings))
	for _, f := range findings {
		ev, err := json.Marshal(f.Evidence)
		if err != nil {
			return 0, fmt.Errorf("analytics: encode evidence: %w", err)
		}
		if err := q.UpsertSuggestion(ctx, dbgen.UpsertSuggestionParams{
			TenantID: tenant, ChannelID: channel, YoutubeVideoID: f.VideoID,
			Rule: f.Rule, Version: f.Version, Evidence: ev,
		}); err != nil {
			return 0, fmt.Errorf("analytics: store suggestion: %w", err)
		}
		keep = append(keep, f.Key())
	}
	if err := q.DeleteSuggestionsExcept(ctx, dbgen.DeleteSuggestionsExceptParams{TenantID: tenant, ChannelID: channel, Keep: keep}); err != nil {
		return 0, fmt.Errorf("analytics: prune suggestions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("analytics: commit suggestions: %w", err)
	}
	return len(findings), nil
}

// Suggestion is a stored suggestion with its rule's title.
type Suggestion struct {
	VideoID   string
	Rule      string
	Version   int32
	Title     string
	Evidence  map[string]any
	Dismissed bool
	UpdatedAt time.Time
}

// Suggestions lists a channel's suggestions: all of them when videoID is
// nil, else those of one video ("" selects the channel-level ones).
func (a *Aggregator) Suggestions(ctx context.Context, tenantID, channelID uuid.UUID, videoID *string, includeDismissed bool) ([]Suggestion, error) {
	arg := dbgen.ListSuggestionsParams{
		TenantID: idconv.ToPg(tenantID), ChannelID: idconv.ToPg(channelID), IncludeDismissed: includeDismissed,
	}
	if videoID != nil {
		arg.YoutubeVideoID = pgtype.Text{String: *videoID, Valid: true}
	}
	rows, err := a.Queries.ListSuggestions(ctx, arg)
	if err != nil {
		return nil, fmt.Errorf("analytics: list suggestions: %w", err)
	}
	out := make([]Suggestion, 0, len(rows))
	for _, r := range rows {
		s := Suggestion{
			VideoID: r.YoutubeVideoID, Rule: r.Rule, Version: r.Version,
			Dismissed: r.Dismissed, UpdatedAt: r.UpdatedAt.Time, Evidence: map[string]any{},
		}
		if info, ok := RuleByName(r.Rule); ok {
			s.Title = info.Title
		}
		_ = json.Unmarshal(r.Evidence, &s.Evidence)
		out = append(out, s)
	}
	return out, nil
}

// SetDismissed dismisses or restores one suggestion.
func (a *Aggregator) SetDismissed(ctx context.Context, tenantID, channelID uuid.UUID, videoID, rule string, dismissed bool) error {
	n, err := a.Queries.SetSuggestionDismissed(ctx, dbgen.SetSuggestionDismissedParams{
		Dismissed: dismissed, TenantID: idconv.ToPg(tenantID), ChannelID: idconv.ToPg(channelID),
		YoutubeVideoID: videoID, Rule: rule,
	})
	if err != nil {
		return fmt.Errorf("analytics: dismiss suggestion: %w", err)
	}
	if n == 0 {
		return ErrSuggestionNotFound
	}
	return nil
}
