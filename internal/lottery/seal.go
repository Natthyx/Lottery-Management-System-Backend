package lottery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Natthyx/lottery-system/internal/models"
)

const sealAlgorithm = "sha256-v1"

// Integrity states reported by GET /events/{id}/results.
const (
	IntegrityVerified = "verified"
	IntegrityMismatch = "mismatch"
	IntegrityUnsealed = "unsealed"
)

// drawDigest is a deterministic SHA-256 over everything that defines a
// draw's outcome: the event, when it ran, how many entered, the winner
// count, the entropy source and every (rank, user) pair in rank order.
// It is computed when the draw commits and stored in the append-only
// lottery_draw_seals table; recomputing it from lottery_draws later
// detects any change to the stored outcome. Publishing or exporting the
// digest at draw time (it is logged and returned) also makes tampering
// detectable by someone who does not trust the database.
func drawDigest(eventID int64, winnerCount int, drawnAt time.Time, rows []models.LotteryDraw) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nevent:%d\nwinner_count:%d\ndrawn_at:%s\nentrants:%d\n",
		sealAlgorithm, eventID, winnerCount, drawnAt.UTC().Format(time.RFC3339Nano), len(rows))
	for _, r := range rows {
		fmt.Fprintf(&b, "%d:%d:%s:%d\n", r.DrawRank, r.WinnerUserID, r.EntropySource, r.TotalEntrants)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
