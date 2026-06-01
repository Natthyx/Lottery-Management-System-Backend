package booking

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/natannan/lottery-system/internal/middleware"
	"github.com/natannan/lottery-system/internal/models"
)

// Handler wires HTTP routes to the booking Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Routes() func(chi.Router) {
	return func(r chi.Router) {
		// Book an event (authenticated users)
		r.Post("/events/{eventID}/book", h.Book)

		// My bookings
		r.Get("/me/bookings", h.MyBookings)
	}
}

// Book godoc
// POST /events/{eventID}/book
// Auth: user JWT required
func (h *Handler) Book(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil {
		respond(w, http.StatusBadRequest, false, nil, "invalid event id")
		return
	}

	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respond(w, http.StatusUnauthorized, false, nil, "unauthenticated")
		return
	}

	if err := h.service.BookWithLock(r.Context(), eventID, userID); err != nil {
		// 409 Conflict is the correct status for "already booked" or "event busy"
		respond(w, http.StatusConflict, false, nil, err.Error())
		return
	}

	respond(w, http.StatusCreated, true, map[string]string{
		"message": "booking confirmed — you are now eligible for the lottery",
	}, "")
}

// MyBookings godoc
// GET /me/bookings
// Auth: user JWT required
func (h *Handler) MyBookings(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respond(w, http.StatusUnauthorized, false, nil, "unauthenticated")
		return
	}

	eventIDs, err := h.service.UserBookings(r.Context(), userID)
	if err != nil {
		respond(w, http.StatusInternalServerError, false, nil, "failed to fetch bookings")
		return
	}

	respond(w, http.StatusOK, true, map[string]interface{}{
		"booked_event_ids": eventIDs,
	}, "")
}

func respond(w http.ResponseWriter, status int, success bool, data interface{}, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(models.APIResponse{ //nolint:errcheck
		Success: success,
		Data:    data,
		Error:   errMsg,
	})
}
