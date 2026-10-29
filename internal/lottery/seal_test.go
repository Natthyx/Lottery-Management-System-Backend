package lottery

import (
	"testing"
	"time"

	"github.com/Natthyx/lottery-system/internal/models"
)

func TestDrawDigest_DetectsAnyChange(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	rows := []models.LotteryDraw{
		{DrawRank: 1, WinnerUserID: 10, EntropySource: entropySource, TotalEntrants: 3},
		{DrawRank: 2, WinnerUserID: 11, EntropySource: entropySource, TotalEntrants: 3},
		{DrawRank: 3, WinnerUserID: 12, EntropySource: entropySource, TotalEntrants: 3},
	}
	base := drawDigest(5, 1, at, rows)
	if base != drawDigest(5, 1, at, rows) {
		t.Fatal("digest is not deterministic")
	}

	swapped := append([]models.LotteryDraw(nil), rows...)
	swapped[0].WinnerUserID, swapped[2].WinnerUserID = 12, 10
	for name, got := range map[string]string{
		"winner swapped":     drawDigest(5, 1, at, swapped),
		"row removed":        drawDigest(5, 1, at, rows[:2]),
		"winner_count moved": drawDigest(5, 2, at, rows),
		"other event":        drawDigest(6, 1, at, rows),
		"time changed":       drawDigest(5, 1, at.Add(time.Microsecond), rows),
	} {
		if got == base {
			t.Errorf("%s: digest unchanged", name)
		}
	}
}
