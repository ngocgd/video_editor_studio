package pipeline

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"loomtale/api/internal/db/idconv"
)

// toPgUUIDs converts a slice of uuid.UUID to the pgtype.UUID slice the
// generated ANY($n::uuid[]) queries expect.
func toPgUUIDs(ids []uuid.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = idconv.ToPg(id)
	}
	return out
}
