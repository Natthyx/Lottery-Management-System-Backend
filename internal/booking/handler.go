package booking

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Natthyx/lottery-system/internal/httpx"
	"github.com/Natthyx/lottery-system/internal/middleware"
)

// Handler wires HTTP routes to the booking Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Book godoc
// POST /events/{eventID}/book  (authenticated)
func (h *Handler) Book(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil || eventID <= 0 {
		httpx.BadRequest(w, "invalid event id")
		return
	}
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httpx.Unauthorized(w, "unauthenticated")
		return
	}

	if err := h.service.BookWithLock(r.Context(), eventID, userID); err != nil {
		switch {
		case errors.Is(err, ErrEventBusy), errors.Is(err, ErrAlreadyBooked),
			errors.Is(err, ErrEventNotOpen), errors.Is(err, ErrEventFull):
			httpx.Conflict(w, err.Error())
		case errors.Is(err, ErrEventNotFound):
			httpx.NotFound(w, err.Error())
		case errors.Is(err, ErrInvalidInput):
			httpx.BadRequest(w, err.Error())
		default:
			httpx.Internal(w, "could not create booking")
		}
		return
	}
	httpx.Respond(w, http.StatusCreated, true, map[string]string{
		"message": "booking confirmed — you are now eligible for the lottery",
	}, "")
}

// MyBookings godoc
// GET /me/bookings  (authenticated)
func (h *Handler) MyBookings(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httpx.Unauthorized(w, "unauthenticated")
		return
	}
	bookings, err := h.service.UserBookings(r.Context(), userID)
	if err != nil {
		httpx.Internal(w, "failed to fetch bookings")
		return
	}
	httpx.Respond(w, http.StatusOK, true, bookings, "")
}

// ListByEvent godoc
// GET /events/{eventID}/bookings  (admin)
func (h *Handler) ListByEvent(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil || eventID <= 0 {
		httpx.BadRequest(w, "invalid event id")
		return
	}
	bookings, err := h.service.GetByEvent(r.Context(), eventID)
	if err != nil {
		httpx.Internal(w, "failed to fetch bookings")
		return
	}
	httpx.Respond(w, http.StatusOK, true, bookings, "")
}
