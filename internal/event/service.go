package event

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Natthyx/lottery-system/internal/models"
)

// Domain errors. Handlers map these to HTTP codes.
var (
	ErrInvalidInput      = errors.New("invalid input")
	ErrEventNotFound     = errors.New("event not found")
	ErrInvalidTransition = errors.New("event status does not permit this transition")
)

const (
	maxTitleLen       = 200
	maxDescriptionLen = 4000
)

// Service holds event business logic.
type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Create validates input and inserts a new event in 'open' status.
func (s *Service) Create(ctx context.Context, req models.CreateEventRequest, createdBy int64) (*models.Event, error) {
	title := strings.TrimSpace(req.Title)
	description := strings.TrimSpace(req.Description)

	if title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}
	if len(title) > maxTitleLen {
		return nil, fmt.Errorf("%w: title too long", ErrInvalidInput)
	}
	if len(description) > maxDescriptionLen {
		return nil, fmt.Errorf("%w: description too long", ErrInvalidInput)
	}
	if req.DrawAt.IsZero() {
		return nil, fmt.Errorf("%w: draw_at is required", ErrInvalidInput)
	}
	if req.DrawAt.Before(time.Now()) {
		return nil, fmt.Errorf("%w: draw_at must be in the future", ErrInvalidInput)
	}
	if req.Capacity < 1 {
		return nil, fmt.Errorf("%w: capacity must be at least 1", ErrInvalidInput)
	}
	if req.WinnerCount < 1 {
		req.WinnerCount = 1
	}
	if req.WinnerCount > req.Capacity {
		return nil, fmt.Errorf("%w: winner_count cannot exceed capacity", ErrInvalidInput)
	}

	var event models.Event
	var status string
	err := s.db.QueryRow(ctx, `
		INSERT INTO events (title, description, capacity, draw_at, winner_count, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, title, description, capacity, draw_at, status, winner_count, created_by, created_at, updated_at
	`, title, description, req.Capacity, req.DrawAt, req.WinnerCount, createdBy).Scan(
		&event.ID, &event.Title, &event.Description, &event.Capacity,
		&event.DrawAt, &status, &event.WinnerCount, &event.CreatedBy,
		&event.CreatedAt, &event.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting event: %w", err)
	}
	event.Status = models.EventStatus(status)
	return &event, nil
}

// GetByID returns an event with its current booking count.
func (s *Service) GetByID(ctx context.Context, id int64) (*models.Event, error) {
	var event models.Event
	var status string
	err := s.db.QueryRow(ctx, `
		SELECT
			e.id, e.title, e.description, e.capacity, e.draw_at,
			e.status, e.winner_count, e.created_by, e.created_at, e.updated_at,
			COUNT(b.id) AS booking_count
		FROM events e
		LEFT JOIN bookings b ON b.event_id = e.id
		WHERE e.id = $1
		GROUP BY e.id
	`, id).Scan(
		&event.ID, &event.Title, &event.Description, &event.Capacity,
		&event.DrawAt, &status, &event.WinnerCount, &event.CreatedBy,
		&event.CreatedAt, &event.UpdatedAt, &event.BookingCount,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("fetching event: %w", err)
	}
	event.Status = models.EventStatus(status)
	return &event, nil
}

// List returns events with pagination metadata.
func (s *Service) List(ctx context.Context, status string, page, limit int) ([]models.Event, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var rows pgx.Rows
	var err error
	if status != "" {
		rows, err = s.db.Query(ctx, `
			SELECT
				e.id, e.title, e.description, e.capacity, e.draw_at,
				e.status, e.winner_count, e.created_by, e.created_at, e.updated_at,
				COUNT(b.id) AS booking_count
			FROM events e
			LEFT JOIN bookings b ON b.event_id = e.id
			WHERE e.status = $1
			GROUP BY e.id
			ORDER BY e.draw_at ASC
			LIMIT $2 OFFSET $3
		`, status, limit, offset)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT
				e.id, e.title, e.description, e.capacity, e.draw_at,
				e.status, e.winner_count, e.created_by, e.created_at, e.updated_at,
				COUNT(b.id) AS booking_count
			FROM events e
			LEFT JOIN bookings b ON b.event_id = e.id
			GROUP BY e.id
			ORDER BY e.draw_at ASC
			LIMIT $1 OFFSET $2
		`, limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()

	events := []models.Event{}
	for rows.Next() {
		var e models.Event
		var st string
		if err := rows.Scan(
			&e.ID, &e.Title, &e.Description, &e.Capacity, &e.DrawAt,
			&st, &e.WinnerCount, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt,
			&e.BookingCount,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning event row: %w", err)
		}
		e.Status = models.EventStatus(st)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating event rows: %w", err)
	}

	var total int
	if status != "" {
		err = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM events WHERE status = $1`, status).Scan(&total)
	} else {
		err = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM events`).Scan(&total)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("counting events: %w", err)
	}

	return events, total, nil
}

// Close transitions an event from 'open' to 'closed'. Idempotent if it
// is already closed (returns the current event), but errors if the
// event was drawn or cancelled.
func (s *Service) Close(ctx context.Context, eventID int64) (*models.Event, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var current string
	err = tx.QueryRow(ctx, `SELECT status FROM events WHERE id = $1 FOR UPDATE`, eventID).Scan(&current)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("loading event: %w", err)
	}

	switch current {
	case string(models.EventStatusOpen):
		if _, err := tx.Exec(ctx,
			`UPDATE events SET status = 'closed' WHERE id = $1`, eventID,
		); err != nil {
			return nil, fmt.Errorf("closing event: %w", err)
		}
	case string(models.EventStatusClosed):
		// Idempotent — already closed.
	default:
		return nil, fmt.Errorf("%w: cannot close event in status %q", ErrInvalidTransition, current)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.GetByID(ctx, eventID)
}

// Cancel transitions an event to 'cancelled' from open or closed.
func (s *Service) Cancel(ctx context.Context, eventID int64) (*models.Event, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var current string
	err = tx.QueryRow(ctx, `SELECT status FROM events WHERE id = $1 FOR UPDATE`, eventID).Scan(&current)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("loading event: %w", err)
	}

	switch current {
	case string(models.EventStatusOpen), string(models.EventStatusClosed):
		if _, err := tx.Exec(ctx,
			`UPDATE events SET status = 'cancelled' WHERE id = $1`, eventID,
		); err != nil {
			return nil, fmt.Errorf("cancelling event: %w", err)
		}
	case string(models.EventStatusCancelled):
		// idempotent
	default:
		return nil, fmt.Errorf("%w: cannot cancel event in status %q", ErrInvalidTransition, current)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.GetByID(ctx, eventID)
}
