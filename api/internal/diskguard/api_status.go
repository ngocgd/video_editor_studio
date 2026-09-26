package diskguard

import (
	"context"
	"log/slog"

	"loomtale/api/internal/httpapi/gen"
)

// APIStatus is the watermark reading as the render and library pages
// show it. A nil watermark or an unreadable disk reports level unknown
// instead of failing the page (Check still refuses renders then).
func APIStatus(ctx context.Context, w *Watermark) gen.DiskStatus {
	if w == nil {
		return gen.DiskStatus{Level: "unknown"}
	}
	s, err := w.Status(ctx)
	if err != nil {
		slog.WarnContext(ctx, "diskguard: cannot read free space", "path", w.Path, "error", err)
		return gen.DiskStatus{
			Level: "unknown", MinFreeBytes: toInt64(w.MinFree), WarnFreeBytes: toInt64(w.WarnFree),
			Message: "Free space on the data disk cannot be read; renders and model downloads are refused until it can.",
		}
	}
	return gen.DiskStatus{
		Level: gen.DiskStatusLevel(s.Level), FreeBytes: toInt64(s.FreeBytes),
		MinFreeBytes: toInt64(s.MinFree), WarnFreeBytes: toInt64(s.WarnFree), Message: s.Message(),
	}
}

func toInt64(v uint64) int64 {
	if v > 1<<62 {
		return 1 << 62
	}
	return int64(v)
}
