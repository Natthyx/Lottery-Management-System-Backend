package booking

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	mrand "math/rand"
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
// the concurrency control around event capacity.
type Service struct {
	db        *pgxpool.Pool
	redis     *redis.Client
	lockTTL   time.Duration
	lockWait  time.Duration
	releaseSh *redis.Script
	gates     *eventGates
}

func NewService(db *pgxpool.Pool, rdb *redis.Client, lockTTL, lockWait time.Duration) *Service {
	if lockTTL <= 0 {
		lockTTL = 5 * time.Second
	}
	if lockWait <= 0 {
		lockWait = 10 * time.Second
	}
	return &Service{
		db:        db,
		redis:     rdb,
		lockTTL:   lockTTL,
		lockWait:  lockWait,
		releaseSh: redis.NewScript(luaReleaseLock),
		gates:     newEventGates(),
	}
}

// BookWithLock serialises bookings per event in three layers and then runs
// the booking transaction:
//
//  1. An in-process FIFO gate per event. Concurrent requests for a hot event
//     queue here in arrival order instead of being rejected, and while they
//     wait they hold no database connection, so one hot event cannot drain
//     the pool for everything else.
//  2. An owner-safe Redis lock, so the single request per replica that got
//     through its gate does not contend with other replicas on the row.
//     Acquisition retries with backoff until the wait budget is spent.
//  3. SELECT ... FOR UPDATE on the event row inside the transaction. This
//     is what guarantees correctness even if layers 1-2 are bypassed.
//
// The whole wait is bounded by lockWait; a request that does not get its
// turn in time fails with ErrEventBusy (retryable).
func (s *Service) BookWithLock(ctx context.Context, eventID, userID int64) error {
	if eventID <= 0 || userID <= 0 {
		return ErrInvalidInput
	}

	waitCtx, cancel := context.WithTimeout(ctx, s.lockWait)
	defer cancel()

	leave, err := s.gates.enter(waitCtx, eventID)
	if err != nil {
		return ErrEventBusy
	}
	defer leave()

	lockKey := fmt.Sprintf("lock:booking:event:%d", eventID)
	token, err := newToken()
	if err != nil {
		return fmt.Errorf("generating lock token: %w", err)
	}
	if err := s.acquireRedisLock(waitCtx, lockKey, token); err != nil {
		return err
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

// acquireRedisLock retries SET NX with jittered exponential backoff until it
// owns the lock or waitCtx expires. Each SET NX gets its own short timeout
// rather than waitCtx, so a deadline can never fire after Redis has applied
// the SET but before we learn we own it (which would orphan the lock until
// its TTL).
func (s *Service) acquireRedisLock(waitCtx context.Context, key, token string) error {
	backoff := 2 * time.Millisecond
	for {
		setCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		acquired, err := s.redis.SetNX(setCtx, key, token, s.lockTTL).Result()
		cancel()
		if err != nil {
			return fmt.Errorf("redis lock acquisition failed: %w", err)
		}
		if acquired {
			return nil
		}
		t := time.NewTimer(backoff/2 + time.Duration(mrand.Int63n(int64(backoff))))
		select {
		case <-waitCtx.Done():
			t.Stop()
			return ErrEventBusy
		case <-t.C:
		}
		if backoff < 50*time.Millisecond {
			backoff *= 2
		}
	}
}

// createBooking runs inside the locks. Duplicate, status and capacity are
// checked under SELECT ... FOR UPDATE so that concurrent bookings across
// replicas (or a lock bypass) cannot oversell the event. A duplicate is
// reported as such even if the event has since filled up or closed.
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

	var already bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM bookings WHERE event_id = $1 AND user_id = $2)`, eventID, userID,
	).Scan(&already); err != nil {
		return fmt.Errorf("checking existing booking: %w", err)
	}
	if already {
		return ErrAlreadyBooked
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
