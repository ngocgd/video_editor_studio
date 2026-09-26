package youtube

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "loomtale/api/internal/db/gen"

	// Embedded zone database: the api and worker images carry no
	// /usr/share/zoneinfo, and the quota day is Pacific time.
	_ "time/tzdata"
)

// Op is a YouTube Data API call that spends quota units.
type Op string

// Quota-spending Data API operations. Token exchange, refresh and revoke
// are OAuth endpoints and cost nothing.
const (
	OpVideosInsert      Op = "videos.insert"
	OpVideosUpdate      Op = "videos.update"
	OpThumbnailsSet     Op = "thumbnails.set"
	OpChannelsList      Op = "channels.list"
	OpPlaylistItemsList Op = "playlistItems.list"
	OpVideosList        Op = "videos.list"
)

// dataAPIBucket is the single daily pool every Data API call draws from.
const dataAPIBucket = "data_api"

// QuotaConfig holds the ledger's limits. Costs start conservative: the
// insert cost stays at 1,600 units until a live upload has been checked
// against the Cloud console quota page, and is lowered only through this
// config, never in code.
type QuotaConfig struct {
	// Project names the Google Cloud project whose daily pool is tracked.
	Project    string `env:"YOUTUBE_QUOTA_PROJECT" envDefault:"default"`
	DailyLimit int    `env:"YOUTUBE_QUOTA_DAILY_LIMIT" envDefault:"10000"`
	InsertCost int    `env:"YOUTUBE_QUOTA_COST_INSERT" envDefault:"1600"`
	// WriteCost covers thumbnails.set and videos.update.
	WriteCost int `env:"YOUTUBE_QUOTA_COST_WRITE" envDefault:"50"`
	// ReadCost covers channels.list, playlistItems.list and videos.list.
	ReadCost int `env:"YOUTUBE_QUOTA_COST_READ" envDefault:"1"`
}

// DefaultQuotaConfig returns the conservative defaults (the same values
// as the env defaults), for tests and callers without env config.
func DefaultQuotaConfig() QuotaConfig {
	return QuotaConfig{Project: "default", DailyLimit: 10000, InsertCost: 1600, WriteCost: 50, ReadCost: 1}
}

// Cost returns the units op is charged in the ledger.
func (c QuotaConfig) Cost(op Op) (int, error) {
	switch op {
	case OpVideosInsert:
		return c.InsertCost, nil
	case OpVideosUpdate, OpThumbnailsSet:
		return c.WriteCost, nil
	case OpChannelsList, OpPlaylistItemsList, OpVideosList:
		return c.ReadCost, nil
	default:
		return 0, fmt.Errorf("youtube: unknown quota op %q", op)
	}
}

var pacific = mustLoadPacific()

func mustLoadPacific() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(fmt.Sprintf("youtube: load Pacific time zone: %v", err))
	}
	return loc
}

// PTDate returns the Pacific-time calendar day t falls in: YouTube resets
// the daily quota at midnight America/Los_Angeles (PST or PDT).
func PTDate(t time.Time) time.Time {
	p := t.In(pacific)
	return time.Date(p.Year(), p.Month(), p.Day(), 0, 0, 0, 0, time.UTC)
}

// NextReset returns the instant of the next Pacific-time midnight after t.
// It is computed in the Pacific zone, so a 23- or 25-hour DST day resets
// at the right wall-clock midnight.
func NextReset(t time.Time) time.Time {
	p := t.In(pacific)
	return time.Date(p.Year(), p.Month(), p.Day()+1, 0, 0, 0, 0, pacific)
}

// QuotaExceededError reports that a call would exceed today's pool.
// Callers snooze until ResetAt instead of calling Google.
type QuotaExceededError struct {
	Op      Op
	ResetAt time.Time
}

func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("youtube: daily quota exhausted for %s; resets at %s", e.Op, e.ResetAt.UTC().Format(time.RFC3339))
}

// QuotaStore is the subset of the generated queries the ledger needs.
type QuotaStore interface {
	ReserveQuotaUnits(ctx context.Context, arg dbgen.ReserveQuotaUnitsParams) (int32, error)
	MarkQuotaExhausted(ctx context.Context, arg dbgen.MarkQuotaExhaustedParams) error
	GetQuotaUnits(ctx context.Context, arg dbgen.GetQuotaUnitsParams) (int32, error)
}

// Ledger is the pre-call quota check. Every Data API call reserves its
// cost first; a call that would pass the daily limit is refused without
// reaching Google.
type Ledger struct {
	Store  QuotaStore
	Config QuotaConfig
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (l *Ledger) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func ptDateParam(t time.Time) pgtype.Date {
	return pgtype.Date{Time: PTDate(t), Valid: true}
}

// Reserve charges op's cost to today's bucket, or returns a
// *QuotaExceededError when that would pass the daily limit.
func (l *Ledger) Reserve(ctx context.Context, op Op) error {
	cost, err := l.Config.Cost(op)
	if err != nil {
		return err
	}
	now := l.now()
	_, err = l.Store.ReserveQuotaUnits(ctx, dbgen.ReserveQuotaUnitsParams{
		Project:    l.Config.Project,
		PtDate:     ptDateParam(now),
		Bucket:     dataAPIBucket,
		Units:      int32(cost),
		DailyLimit: int32(l.Config.DailyLimit),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return &QuotaExceededError{Op: op, ResetAt: NextReset(now)}
	}
	if err != nil {
		return fmt.Errorf("youtube: reserve quota for %s: %w", op, err)
	}
	return nil
}

// MarkExhausted records that Google itself answered quotaExceeded, so the
// rest of the Pacific day is refused locally. It returns the error the
// caller should surface.
func (l *Ledger) MarkExhausted(ctx context.Context, op Op) error {
	now := l.now()
	if err := l.Store.MarkQuotaExhausted(ctx, dbgen.MarkQuotaExhaustedParams{
		Project:    l.Config.Project,
		PtDate:     ptDateParam(now),
		Bucket:     dataAPIBucket,
		DailyLimit: int32(l.Config.DailyLimit),
	}); err != nil {
		return fmt.Errorf("youtube: mark quota exhausted: %w", err)
	}
	return &QuotaExceededError{Op: op, ResetAt: NextReset(now)}
}

// QuotaUsage is today's ledger state, for the UI.
type QuotaUsage struct {
	Used    int
	Limit   int
	ResetAt time.Time
}

// Usage returns today's units spent, the limit and the next reset.
func (l *Ledger) Usage(ctx context.Context) (QuotaUsage, error) {
	now := l.now()
	used, err := l.Store.GetQuotaUnits(ctx, dbgen.GetQuotaUnitsParams{
		Project: l.Config.Project,
		PtDate:  ptDateParam(now),
		Bucket:  dataAPIBucket,
	})
	if err != nil {
		return QuotaUsage{}, fmt.Errorf("youtube: read quota usage: %w", err)
	}
	return QuotaUsage{Used: int(used), Limit: l.Config.DailyLimit, ResetAt: NextReset(now)}, nil
}
