package lottery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/Natthyx/lottery-system/internal/models"
)

// Domain errors.
var (
	ErrEventNotFound      = errors.New("event not found")
	ErrEventNotClosed     = errors.New("event must be closed before drawing")
	ErrEventAlreadyDrawn  = errors.New("event has already been drawn")
	ErrEventCancelled     = errors.New("event has been cancelled")
	ErrNoParticipants     = errors.New("no participants in the lottery pool")
	ErrResultsUnavailable = errors.New("no draw results available for this event")
)

const entropySource = "crypto/rand+fisher-yates"

// Service orchestrates the lottery draw end-to-end:
//  1. Lock the event row (SELECT ... FOR UPDATE) inside a transaction.
//  2. Require status='closed' so no new bookings can arrive.
//  3. Fetch the participant pool inside the same transaction.
//  4. Run a cryptographically fair Fisher–Yates selection.
//  5. Insert audit-log rows and flip status='drawn'.
//  6. Commit.
//
// Steps 1-6 happen in one transaction. Either everyone sees a fair draw
// committed to the audit log, or nothing happens.
type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// RunDraw executes the lottery for a closed event and returns the result.
func (s *Service) RunDraw(ctx context.Context, eventID int64) (*models.DrawResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin draw tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 1. Lock the event row.
	var title, status string
	var winnerCount int
	err = tx.QueryRow(ctx, `
		SELECT title, status, winner_count
		FROM events
		WHERE id = $1
		FOR UPDATE
	`, eventID).Scan(&title, &status, &winnerCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("locking event: %w", err)
	}

	// 2. Enforce status machine.
	switch status {
	case string(models.EventStatusClosed):
		// ok
	case string(models.EventStatusDrawn):
		return nil, ErrEventAlreadyDrawn
	case string(models.EventStatusCancelled):
		return nil, ErrEventCancelled
	default: // open
		return nil, fmt.Errorf("%w (current status: %s)", ErrEventNotClosed, status)
	}

	// 3. Fetch participants under the same lock.
	rows, err := tx.Query(ctx, `
		SELECT user_id FROM bookings
		WHERE event_id = $1
		ORDER BY booked_at ASC
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("fetching participants: %w", err)
	}
	participants := make([]int64, 0, 64)
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning participant: %w", err)
		}
		participants = append(participants, uid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating participants: %w", err)
	}
	if len(participants) == 0 {
		return nil, ErrNoParticipants
	}

	// 4. Cryptographic Fisher-Yates selection.
	winners, waitlist, err := SelectWinners(participants, winnerCount)
	if err != nil {
		return nil, fmt.Errorf("lottery selection: %w", err)
	}

	log.Info().
		Int64("event_id", eventID).
		Int("participant_count", len(participants)).
		Int("winner_count", len(winners)).
		Msg("running lottery draw")

	// 5. Persist audit log + flip status.
	drawnAt := time.Now().UTC()
	totalEntrants := len(participants)
	winnerRecords := make([]models.LotteryDraw, 0, len(winners))
	waitlistRecords := make([]models.LotteryDraw, 0, len(waitlist))

	insert := func(userID int64, rank int) (models.LotteryDraw, error) {
		var d models.LotteryDraw
		err := tx.QueryRow(ctx, `
			INSERT INTO lottery_draws (event_id, winner_user_id, draw_rank, total_entrants, entropy_source, drawn_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, event_id, winner_user_id, draw_rank, entropy_source, total_entrants, drawn_at
		`, eventID, userID, rank, totalEntrants, entropySource, drawnAt).Scan(
			&d.ID, &d.EventID, &d.WinnerUserID, &d.DrawRank,
			&d.EntropySource, &d.TotalEntrants, &d.DrawnAt,
		)
		return d, err
	}

	for i, uid := range winners {
		d, err := insert(uid, i+1)
		if err != nil {
			return nil, fmt.Errorf("inserting winner rank %d: %w", i+1, err)
		}
		winnerRecords = append(winnerRecords, d)
	}
	for i, uid := range waitlist {
		rank := len(winners) + i + 1
		d, err := insert(uid, rank)
		if err != nil {
			return nil, fmt.Errorf("inserting waitlist rank %d: %w", rank, err)
		}
		waitlistRecords = append(waitlistRecords, d)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE events SET status = 'drawn' WHERE id = $1`, eventID,
	); err != nil {
		return nil, fmt.Errorf("marking event as drawn: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit draw tx: %w", err)
	}

	log.Info().
		Int64("event_id", eventID).
		Int("winners", len(winnerRecords)).
		Int("waitlist", len(waitlistRecords)).
		Msg("lottery draw completed")

	return &models.DrawResult{
		EventID:       eventID,
		EventTitle:    title,
		TotalEntrants: totalEntrants,
		Winners:       winnerRecords,
		Waitlist:      waitlistRecords,
		DrawnAt:       drawnAt,
	}, nil
}

// GetResults loads the committed draw from the audit log and splits it
// into winners and waitlist by comparing draw_rank to the event's
// winner_count snapshot.
func (s *Service) GetResults(ctx context.Context, eventID int64) (*models.DrawResult, error) {
	var title string
	var winnerCount int
	err := s.db.QueryRow(ctx, `
		SELECT title, winner_count FROM events
		WHERE id = $1 AND status = 'drawn'
	`, eventID).Scan(&title, &winnerCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrResultsUnavailable
		}
		return nil, fmt.Errorf("fetching event: %w", err)
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

	result := &models.DrawResult{
		EventID:    eventID,
		EventTitle: title,
		Winners:    []models.LotteryDraw{},
		Waitlist:   []models.LotteryDraw{},
	}
	for rows.Next() {
		var d models.LotteryDraw
		if err := rows.Scan(
			&d.ID, &d.EventID, &d.WinnerUserID, &d.DrawRank,
			&d.EntropySource, &d.TotalEntrants, &d.DrawnAt,
		); err != nil {
			return nil, fmt.Errorf("scanning draw row: %w", err)
		}
		if result.DrawnAt.IsZero() {
			result.DrawnAt = d.DrawnAt
			result.TotalEntrants = d.TotalEntrants
		}
		if d.DrawRank <= winnerCount {
			result.Winners = append(result.Winners, d)
		} else {
			result.Waitlist = append(result.Waitlist, d)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating draw rows: %w", err)
	}
	return result, nil
}
