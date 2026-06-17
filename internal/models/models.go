package models

import "time"

// ── User ─────────────────────────────────────────────────────────────────────

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	FullName     string    `json:"full_name"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"` // never serialised to JSON
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ── Event ────────────────────────────────────────────────────────────────────

type EventStatus string

const (
	EventStatusOpen      EventStatus = "open"
	EventStatusClosed    EventStatus = "closed"
	EventStatusDrawn     EventStatus = "drawn"
	EventStatusCancelled EventStatus = "cancelled"
)

type Event struct {
	ID           int64       `json:"id"`
	Title        string      `json:"title"`
	Description  string      `json:"description"`
	Capacity     int         `json:"capacity"`
	DrawAt       time.Time   `json:"draw_at"`
	Status       EventStatus `json:"status"`
	WinnerCount  int         `json:"winner_count"`
	CreatedBy    int64       `json:"created_by"`
	BookingCount int         `json:"booking_count,omitempty"` // populated on reads
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// ── Booking ──────────────────────────────────────────────────────────────────

type Booking struct {
	ID       int64     `json:"id"`
	EventID  int64     `json:"event_id"`
	UserID   int64     `json:"user_id"`
	BookedAt time.Time `json:"booked_at"`
}

// ── Lottery Draw (audit log entry) ───────────────────────────────────────────

type LotteryDraw struct {
	ID            int64     `json:"id"`
	EventID       int64     `json:"event_id"`
	WinnerUserID  int64     `json:"winner_user_id"`
	DrawRank      int       `json:"draw_rank"` // 1 = winner, 2+ = waitlist
	EntropySource string    `json:"entropy_source"`
	TotalEntrants int       `json:"total_entrants"`
	DrawnAt       time.Time `json:"drawn_at"`
}

// DrawResult wraps the full outcome of a lottery draw, returned from the API.
type DrawResult struct {
	EventID       int64         `json:"event_id"`
	EventTitle    string        `json:"event_title"`
	TotalEntrants int           `json:"total_entrants"`
	Winners       []LotteryDraw `json:"winners"`
	Waitlist      []LotteryDraw `json:"waitlist"`
	DrawnAt       time.Time     `json:"drawn_at"`
}

// ── Request / Response DTOs ──────────────────────────────────────────────────

type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type CreateEventRequest struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Capacity    int       `json:"capacity"`
	DrawAt      time.Time `json:"draw_at"`
	WinnerCount int       `json:"winner_count"`
}

type UpdateEventRequest struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Capacity    int       `json:"capacity"`
	DrawAt      time.Time `json:"draw_at"`
}

// ── API envelope ─────────────────────────────────────────────────────────────

// APIResponse is the standard JSON envelope for every endpoint.
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Meta    *Meta       `json:"meta,omitempty"`
}

// Meta carries pagination info for list endpoints.
type Meta struct {
	Total int `json:"total"`
	Page  int `json:"page"`
	Limit int `json:"limit"`
}
