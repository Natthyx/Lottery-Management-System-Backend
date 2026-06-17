package booking

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/Natthyx/lottery-system/internal/models"
	"github.com/Natthyx/lottery-system/internal/pgerr"
)

// Domain errors.
var (
	ErrEventBusy     = errors.New("event is busy, please retry")
	ErrEventNotFound = errors.New("event not found")
	ErrEventNotOpen  = errors.New("event is not open for booking")
	ErrEventFull     = errors.New("event is fully booked")
	ErrAlreadyBooked = errors.New("you have already booked this event")
	ErrInvalidInput  = errors.New("invalid input")
)

// luaReleaseLock atomically deletes the lock key only if its value matches
// the supplied token. This prevents a request whose TTL expired from
// accidentally releasing another holder's lock.
//
// KEYS[1] = lock key, ARGV[1] = our token
const luaReleaseLock = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
else
	return 0
end`

// Service handles all booking logic. The critical path is BookWithLock —
// the distributed concurrency control around event capacity.
type Service struct {
	db        *pgxpool.Pool
	redis     *redis.Client
	lockTTL   time.Duration
	releaseSh *redis.Script
}

func NewService(db *pgxpool.Pool, rdb *redis.Client, lockTTL time.Duration) *Service {
	if lockTTL <= 0 {
		lockTTL = 5 * time.Second
	}
	return &Service{
		db:        db,
		redis:     rdb,
		lockTTL:   lockTTL,
		releaseSh: redis.NewScript(luaReleaseLock),
	}
}

// BookWithLock acquires a Redis distributed lock on the event then runs
// the booking inside a database transaction. The lock is owner-safe:
// release is a Lua "compare-and-delete" against a per-request token, so
// a request whose TTL expired cannot release another request's lock.
func (s *Service) BookWithLock(ctx context.Context, eventID, userID int64) error {
	if eventID <= 0 || userID <= 0 {
		return ErrInvalidInput
	}

	lockKey := fmt.Sprintf("lock:booking:event:%d", eventID)
	token, err := newToken()
	if err != nil {
		return fmt.Errorf("generating lock token: %w", err)
	}

	acquired, err := s.redis.SetNX(ctx, lockKey, token, s.lockTTL).Result()
	if err != nil {
		return fmt.Errorf("redis lock acquisition failed: %w", err)
	}
	if !acquired {
		return ErrEventBusy
	}

	defer func() {
		// Owner-safe release: only deletes if we still own it.
		// Use a fresh background context with a short timeout so that
		// release still happens even if the caller's context was
		// cancelled (e.g. client disconnected).
		relCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := s.releaseSh.Run(relCtx, s.redis, []string{lockKey}, token).Result(); err != nil && !errors.Is(err, redis.Nil) {
			log.Warn().Err(err).Str("key", lockKey).Msg("failed to release Redis lock")
		}
	}()

	return s.createBooking(ctx, eventID, userID)
}

// createBooking runs inside the Redis lock. Capacity and status are
// re-checked under a SELECT ... FOR UPDATE so that concurrent bookings
// across replicas (or a Redis-lock bypass) cannot oversell the event.
func (s *Service) createBooking(ctx context.Context, eventID, userID int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var status string
	var capacity int
	err = tx.QueryRow(ctx,
		`SELECT status, capacity FROM events WHERE id = $1 FOR UPDATE`,
		eventID,
	).Scan(&status, &capacity)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEventNotFound
		}
		return fmt.Errorf("fetching event: %w", err)
	}

	if status != string(models.EventStatusOpen) {
		return fmt.Errorf("%w (current status: %s)", ErrEventNotOpen, status)
	}

	var booked int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM bookings WHERE event_id = $1`, eventID,
	).Scan(&booked); err != nil {
		return fmt.Errorf("counting bookings: %w", err)
	}
	if booked >= capacity {
		return fmt.Errorf("%w (%d/%d)", ErrEventFull, booked, capacity)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO bookings (event_id, user_id) VALUES ($1, $2)`,
		eventID, userID,
	)
	if err != nil {
		if pgerr.IsUnique(err) {
			return ErrAlreadyBooked
		}
		if pgerr.IsForeignKey(err) {
			return ErrEventNotFound
		}
		return fmt.Errorf("inserting booking: %w", err)
	}

	return tx.Commit(ctx)
}

// GetByEvent returns all bookings for an event (admin use).
func (s *Service) GetByEvent(ctx context.Context, eventID int64) ([]models.Booking, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_id, user_id, booked_at
		FROM bookings
		WHERE event_id = $1
		ORDER BY booked_at ASC
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("querying bookings: %w", err)
	}
	defer rows.Close()

	bookings := []models.Booking{}
	for rows.Next() {
		var b models.Booking
		if err := rows.Scan(&b.ID, &b.EventID, &b.UserID, &b.BookedAt); err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bookings, nil
}

// UserBookings returns all bookings made by a user.
func (s *Service) UserBookings(ctx context.Context, userID int64) ([]models.Booking, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_id, user_id, booked_at
		FROM bookings
		WHERE user_id = $1
		ORDER BY booked_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("querying user bookings: %w", err)
	}
	defer rows.Close()

	bookings := []models.Booking{}
	for rows.Next() {
		var b models.Booking
		if err := rows.Scan(&b.ID, &b.EventID, &b.UserID, &b.BookedAt); err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bookings, nil
}

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
