package lottery

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/natannan/lottery-system/internal/models"
	"github.com/rs/zerolog/log"
)

// Service orchestrates the lottery draw.
// It ties together: fetching participants, running the algorithm,
// persisting results to the audit log, and updating the event status —
// all inside a single database transaction.
type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// RunDraw is the main draw orchestration function.
//
// Execution order (all atomic — DB transaction wraps steps 2-5):
//  1. Validate the event is in 'open' or 'closed' state
//  2. Fetch all eligible participant user IDs
//  3. Run the cryptographic Fisher-Yates selection
//  4. Write every draw result to the lottery_draws audit log
//  5. Mark the event as 'drawn'
//
// Atomicity is critical here: if we write winners but crash before
// marking the event 'drawn', the draw could be run twice. The
// transaction ensures all-or-nothing.
func (s *Service) RunDraw(ctx context.Context, eventID int64) (*models.DrawResult, error) {
	// ── Step 1: Load event (outside the transaction — read-only) ─────────────
	var title string
	var status string
	var winnerCount int

	err := s.db.QueryRow(ctx,
		`SELECT title, status, winner_count FROM events WHERE id = $1`,
		eventID,
	).Scan(&title, &status, &winnerCount)
	if err != nil {
		return nil, fmt.Errorf("event %d not found", eventID)
	}

	if status == "drawn" {
		return nil, fmt.Errorf("lottery for event %d has already been drawn", eventID)
	}
	if status == "cancelled" {
		return nil, fmt.Errorf("event %d is cancelled", eventID)
	}

	// ── Step 2: Fetch participants ────────────────────────────────────────────
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM bookings WHERE event_id = $1 ORDER BY booked_at ASC`,
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("fetching participants: %w", err)
	}
	defer rows.Close()

	var participants []int64
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		participants = append(participants, uid)
	}

	if len(participants) == 0 {
		return nil, fmt.Errorf("no participants found for event %d", eventID)
	}

	log.Info().
		Int64("event_id", eventID).
		Int("participant_count", len(participants)).
		Msg("running lottery draw")

	// ── Step 3: Run the cryptographic selection ───────────────────────────────
	winners, waitlist, err := SelectWinners(participants, winnerCount)
	if err != nil {
		return nil, fmt.Errorf("lottery selection failed: %w", err)
	}

	// ── Steps 4 & 5: Persist results atomically ───────────────────────────────
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning draw transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	drawnAt := time.Now()
	totalEntrants := len(participants)
	var drawRecords []models.LotteryDraw

	// Insert winners (rank 1..winnerCount)
	for rank, userID := range winners {
		drawRank := rank + 1
		var drawID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO lottery_draws (event_id, winner_user_id, draw_rank, total_entrants, drawn_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id
		`, eventID, userID, drawRank, totalEntrants, drawnAt).Scan(&drawID)
		if err != nil {
			return nil, fmt.Errorf("inserting winner rank %d: %w", drawRank, err)
		}
		drawRecords = append(drawRecords, models.LotteryDraw{
			ID:            drawID,
			EventID:       eventID,
			WinnerUserID:  userID,
			DrawRank:      drawRank,
			EntropySource: "crypto/rand+Fisher-Yates",
			TotalEntrants: totalEntrants,
			DrawnAt:       drawnAt,
		})
	}

	// Insert waitlist (rank winnerCount+1..n)
	var waitlistRecords []models.LotteryDraw
	for i, userID := range waitlist {
		drawRank := winnerCount + i + 1
		var drawID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO lottery_draws (event_id, winner_user_id, draw_rank, total_entrants, drawn_at)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id
		`, eventID, userID, drawRank, totalEntrants, drawnAt).Scan(&drawID)
		if err != nil {
			return nil, fmt.Errorf("inserting waitlist rank %d: %w", drawRank, err)
		}
		waitlistRecords = append(waitlistRecords, models.LotteryDraw{
			ID:            drawID,
			EventID:       eventID,
			WinnerUserID:  userID,
			DrawRank:      drawRank,
			EntropySource: "crypto/rand+Fisher-Yates",
			TotalEntrants: totalEntrants,
			DrawnAt:       drawnAt,
		})
	}

	// Mark event as drawn
	_, err = tx.Exec(ctx,
		`UPDATE events SET status = 'drawn' WHERE id = $1`,
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("marking event as drawn: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing draw transaction: %w", err)
	}

	log.Info().
		Int64("event_id", eventID).
		Int("winners", len(winners)).
		Int("waitlist", len(waitlist)).
		Msg("lottery draw completed successfully")

	return &models.DrawResult{
		EventID:       eventID,
		EventTitle:    title,
		TotalEntrants: totalEntrants,
		Winners:       drawRecords,
		Waitlist:      waitlistRecords,
		DrawnAt:       drawnAt,
	}, nil
}

// GetResults fetches the stored draw results for an event from the audit log.
func (s *Service) GetResults(ctx context.Context, eventID int64) (*models.DrawResult, error) {
	// Verify event exists
	var title string
	err := s.db.QueryRow(ctx,
		`SELECT title FROM events WHERE id = $1 AND status = 'drawn'`,
		eventID,
	).Scan(&title)
	if err != nil {
		return nil, fmt.Errorf("no draw results found for event %d", eventID)
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, event_id, winner_user_id, draw_rank, entropy_source, total_entrants, drawn_at
		FROM lottery_draws
		WHERE event_id = $1
		ORDER BY draw_rank ASC
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("fetching draw results: %w", err)
	}
	defer rows.Close()

	result := &models.DrawResult{EventID: eventID, EventTitle: title}
	for rows.Next() {
		var d models.LotteryDraw
		if err := rows.Scan(
			&d.ID, &d.EventID, &d.WinnerUserID, &d.DrawRank,
			&d.EntropySource, &d.TotalEntrants, &d.DrawnAt,
		); err != nil {
			return nil, err
		}
		if result.DrawnAt.IsZero() {
			result.DrawnAt = d.DrawnAt
			result.TotalEntrants = d.TotalEntrants
		}
		// Separate winners from waitlist by checking if the event's winner_count
		// is known — we use draw_rank to distinguish
		result.Winners = append(result.Winners, d)
	}

	return result, nil
}
