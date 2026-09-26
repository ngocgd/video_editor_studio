package youtube

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	dbgen "loomtale/api/internal/db/gen"
)

// memQuota mirrors the ledger SQL: a conditional add that returns no row
// when the limit would be passed, and a GREATEST-style exhaust.
type memQuota struct{ units map[string]int32 }

func key(project string, d time.Time, bucket string) string {
	return project + "|" + d.Format("2006-01-02") + "|" + bucket
}

func (m *memQuota) ReserveQuotaUnits(_ context.Context, a dbgen.ReserveQuotaUnitsParams) (int32, error) {
	k := key(a.Project, a.PtDate.Time, a.Bucket)
	if m.units[k]+a.Units > a.DailyLimit {
		return 0, pgx.ErrNoRows
	}
	m.units[k] += a.Units
	return m.units[k], nil
}

func (m *memQuota) MarkQuotaExhausted(_ context.Context, a dbgen.MarkQuotaExhaustedParams) error {
	k := key(a.Project, a.PtDate.Time, a.Bucket)
	m.units[k] = max(m.units[k], a.DailyLimit)
	return nil
}

func (m *memQuota) GetQuotaUnits(_ context.Context, a dbgen.GetQuotaUnitsParams) (int32, error) {
	return m.units[key(a.Project, a.PtDate.Time, a.Bucket)], nil
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPTDateAndNextReset(t *testing.T) {
	cases := []struct {
		name, at, wantDate, wantReset string
	}{
		// 07:59Z is 23:59 PST the previous day; 08:00Z is PST midnight.
		{"winter before midnight", "2026-01-15T07:59:00Z", "2026-01-14", "2026-01-15T08:00:00Z"},
		{"winter at midnight", "2026-01-15T08:00:00Z", "2026-01-15", "2026-01-16T08:00:00Z"},
		// PDT is UTC-7, so midnight is 07:00Z.
		{"summer before midnight", "2026-07-01T06:59:59Z", "2026-06-30", "2026-07-01T07:00:00Z"},
		{"summer after midnight", "2026-07-01T07:00:00Z", "2026-07-01", "2026-07-02T07:00:00Z"},
		// DST starts 2026-03-08: that day is 23 hours long.
		{"spring forward day", "2026-03-08T12:00:00Z", "2026-03-08", "2026-03-09T07:00:00Z"},
		// DST ends 2026-11-01: that day is 25 hours long.
		{"fall back day", "2026-11-01T12:00:00Z", "2026-11-01", "2026-11-02T08:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			at := mustTime(t, c.at)
			if got := PTDate(at).Format("2006-01-02"); got != c.wantDate {
				t.Errorf("PTDate = %s, want %s", got, c.wantDate)
			}
			if got := NextReset(at).UTC().Format(time.RFC3339); got != c.wantReset {
				t.Errorf("NextReset = %s, want %s", got, c.wantReset)
			}
		})
	}
}

func TestQuotaConfigCosts(t *testing.T) {
	cfg := DefaultQuotaConfig()
	want := map[Op]int{
		OpVideosInsert: 1600, OpVideosUpdate: 50, OpThumbnailsSet: 50,
		OpChannelsList: 1, OpPlaylistItemsList: 1, OpVideosList: 1,
	}
	for op, units := range want {
		got, err := cfg.Cost(op)
		if err != nil || got != units {
			t.Errorf("Cost(%s) = %d, %v; want %d", op, got, err, units)
		}
	}
	if _, err := cfg.Cost("search.list"); err == nil {
		t.Error("unknown op should be an error, not free")
	}
}

func TestLedgerAllowsSixInsertsThenRefusesUntilReset(t *testing.T) {
	store := &memQuota{units: map[string]int32{}}
	now := mustTime(t, "2026-09-26T20:00:00Z")
	l := &Ledger{Store: store, Config: DefaultQuotaConfig(), Now: func() time.Time { return now }}
	ctx := context.Background()

	for i := range 6 {
		if err := l.Reserve(ctx, OpVideosInsert); err != nil {
			t.Fatalf("insert %d: %v", i+1, err)
		}
	}
	// 9,600 used: a seventh insert would pass 10,000.
	err := l.Reserve(ctx, OpVideosInsert)
	var qe *QuotaExceededError
	if !errors.As(err, &qe) {
		t.Fatalf("seventh insert: got %v, want QuotaExceededError", err)
	}
	if want := mustTime(t, "2026-09-27T07:00:00Z"); !qe.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %s, want %s", qe.ResetAt.UTC(), want)
	}
	// Smaller calls still fit in the remaining 400 units.
	if err := l.Reserve(ctx, OpThumbnailsSet); err != nil {
		t.Fatalf("thumbnail after refused insert: %v", err)
	}
	u, err := l.Usage(ctx)
	if err != nil || u.Used != 9650 || u.Limit != 10000 {
		t.Fatalf("Usage = %+v, %v; want 9650/10000", u, err)
	}

	// After Pacific midnight the pool is fresh.
	now = mustTime(t, "2026-09-27T07:00:01Z")
	if err := l.Reserve(ctx, OpVideosInsert); err != nil {
		t.Fatalf("insert after reset: %v", err)
	}
}

func TestLedgerMarkExhaustedBlocksRestOfDay(t *testing.T) {
	store := &memQuota{units: map[string]int32{}}
	now := mustTime(t, "2026-09-26T20:00:00Z")
	l := &Ledger{Store: store, Config: DefaultQuotaConfig(), Now: func() time.Time { return now }}
	ctx := context.Background()

	var qe *QuotaExceededError
	if err := l.MarkExhausted(ctx, OpVideosInsert); !errors.As(err, &qe) {
		t.Fatalf("MarkExhausted returned %v, want QuotaExceededError", err)
	}
	if err := l.Reserve(ctx, OpChannelsList); !errors.As(err, &qe) {
		t.Fatalf("read after exhaustion: got %v, want QuotaExceededError", err)
	}
}
