package event

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/natannan/lottery-system/internal/models"
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

func (s *Service) Create(ctx context.Context, req models.CreateEventRequest, createdBy int64) (*models.Event, error) {
	if req.DrawAt.Before(time.Now()) {
		return nil, fmt.Errorf("draw_at must be in the future")
	}
	if req.Capacity < 1 {
		return nil, fmt.Errorf("capacity must be at least 1")
	}
	if req.WinnerCount < 1 {
		req.WinnerCount = 1
	}
	if req.WinnerCount > req.Capacity {
		return nil, fmt.Errorf("winner_count cannot exceed capacity")
	}

	var event models.Event
	var status string
	err := s.db.QueryRow(ctx, `
		INSERT INTO events (title, description, capacity, draw_at, winner_count, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, title, description, capacity, draw_at, status, winner_count, created_by, created_at, updated_at
	`, req.Title, req.Description, req.Capacity, req.DrawAt, req.WinnerCount, createdBy).Scan(
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
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("event not found")
	}
	if err != nil {
		return nil, fmt.Errorf("fetching event: %w", err)
	}
	event.Status = models.EventStatus(status)
	return &event, nil
}

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

	var events []models.Event
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

	// CRITICAL: always check rows.Err() after iteration
	// If the DB connection dropped mid-query, rows.Next() returns false
	// but rows.Err() will hold the actual error
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating event rows: %w", err)
	}

	var total int
	if status != "" {
		s.db.QueryRow(ctx, `SELECT COUNT(*) FROM events WHERE status = $1`, status).Scan(&total) //nolint:errcheck
	} else {
		s.db.QueryRow(ctx, `SELECT COUNT(*) FROM events`).Scan(&total) //nolint:errcheck
	}

	if events == nil {
		events = []models.Event{}
	}

	return events, total, nil
}

func (s *Service) Close(ctx context.Context, eventID int64) error {
	result, err := s.db.Exec(ctx,
		`UPDATE events SET status = 'closed' WHERE id = $1 AND status = 'open'`,
		eventID,
	)
	if err != nil {
		return fmt.Errorf("closing event: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("event is not open or does not exist")
	}
	return nil
}

func (s *Service) MarkDrawn(ctx context.Context, eventID int64) error {
	_, err := s.db.Exec(ctx,
		`UPDATE events SET status = 'drawn' WHERE id = $1 AND status = 'closed'`,
		eventID,
	)
	return err
}