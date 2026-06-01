package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Service handles all booking logic.
// The critical path here is BookWithLock — the distributed concurrency control.
type Service struct {
	db      *pgxpool.Pool
	redis   *redis.Client
	lockTTL time.Duration
}

func NewService(db *pgxpool.Pool, redis *redis.Client, lockTTL time.Duration) *Service {
	return &Service{db: db, redis: redis, lockTTL: lockTTL}
}

// BookWithLock is the core method. It:
//  1. Acquires a Redis distributed lock on the event
//  2. Checks event is open and has capacity
//  3. Inserts the booking inside that lock
//  4. Releases the lock
//
// WHY Redis lock here:
//   Without this, 1000 concurrent users can all read "1 slot remaining"
//   and all write a booking — overselling the event. Redis SETNX is atomic:
//   only one goroutine across ALL app replicas can hold the lock at a time.
//
// WHY TTL on the lock:
//   If the process crashes mid-execution, the lock expires automatically
//   after lockTTL instead of hanging forever.
func (s *Service) BookWithLock(ctx context.Context, eventID, userID int64) error {
	lockKey := fmt.Sprintf("lock:booking:event:%d", eventID)
	lockValue := fmt.Sprintf("%d", userID) // helps with debugging — who holds the lock

	// ── Step 1: Acquire distributed lock ─────────────────────────────────────
	// SetNX = SET if Not eXists. Atomic in Redis.
	acquired, err := s.redis.SetNX(ctx, lockKey, lockValue, s.lockTTL).Result()
	if err != nil {
		return fmt.Errorf("redis lock acquisition failed: %w", err)
	}
	if !acquired {
		// Another request is currently booking this event. Tell the client to retry.
		return fmt.Errorf("event is busy, please try again in a moment")
	}

	// ── Step 2: Always release the lock when done ─────────────────────────────
	// defer runs even if we return early due to an error below.
	defer func() {
		if err := s.redis.Del(ctx, lockKey).Err(); err != nil {
			log.Error().Err(err).Str("key", lockKey).Msg("failed to release Redis lock")
		}
	}()

	// ── Step 3: Safe zone — only one goroutine reaches here per event ─────────
	return s.createBooking(ctx, eventID, userID)
}

// createBooking runs inside the Redis lock. It validates capacity and writes
// the booking in a single database transaction.
func (s *Service) createBooking(ctx context.Context, eventID, userID int64) error {
	// Use a DB transaction so that the capacity check and the insert are atomic.
	// Without the transaction, two goroutines could both pass the capacity check
	// before either writes — the Redis lock prevents this at the application level,
	// but the DB transaction is a second line of defence.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — rollback is a no-op after commit

	// Fetch and lock the event row FOR UPDATE.
	// This prevents a concurrent DB-level race even if the Redis lock is somehow
	// bypassed (e.g. Redis failover). Defence in depth.
	var status string
	var capacity int
	err = tx.QueryRow(ctx,
		`SELECT status, capacity FROM events WHERE id = $1 FOR UPDATE`,
		eventID,
	).Scan(&status, &capacity)
	if err != nil {
		return fmt.Errorf("event not found: %w", err)
	}

	if status != "open" {
		return fmt.Errorf("event is not open for booking (status: %s)", status)
	}

	// Count existing bookings
	var booked int
	tx.QueryRow(ctx, //nolint:errcheck
		`SELECT COUNT(*) FROM bookings WHERE event_id = $1`, eventID,
	).Scan(&booked)

	if booked >= capacity {
		return fmt.Errorf("event is fully booked (%d/%d)", booked, capacity)
	}

	// Insert the booking.
	// The UNIQUE(event_id, user_id) constraint in the DB is our final safety net
	// against duplicate bookings — it will return an error if the user already booked.
	_, err = tx.Exec(ctx,
		`INSERT INTO bookings (event_id, user_id) VALUES ($1, $2)`,
		eventID, userID,
	)
	if err != nil {
		// Check for the unique violation — give a clear message
		if isUniqueViolation(err) {
			return fmt.Errorf("you have already booked this event")
		}
		return fmt.Errorf("inserting booking: %w", err)
	}

	return tx.Commit(ctx)
}

// GetByEvent returns all bookings for a given event (admin use).
func (s *Service) GetByEvent(ctx context.Context, eventID int64) ([]int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM bookings WHERE event_id = $1 ORDER BY booked_at ASC`,
		eventID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying bookings: %w", err)
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, id)
	}
	return userIDs, nil
}

// UserBookings returns all event IDs a user has booked.
func (s *Service) UserBookings(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT event_id FROM bookings WHERE user_id = $1 ORDER BY booked_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying user bookings: %w", err)
	}
	defer rows.Close()

	var eventIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		eventIDs = append(eventIDs, id)
	}
	return eventIDs, nil
}

// isUniqueViolation checks if a pgx error is a PostgreSQL unique constraint violation.
// PG error code 23505 = unique_violation.
func isUniqueViolation(err error) bool {
	return err != nil && len(err.Error()) > 0 &&
		(contains(err.Error(), "23505") || contains(err.Error(), "unique"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
