package event

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Natthyx/lottery-system/internal/httpx"
	"github.com/Natthyx/lottery-system/internal/middleware"
	"github.com/Natthyx/lottery-system/internal/models"
)

// Handler wires HTTP routes to the event Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Create godoc
// POST /events  (admin)
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateEventRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.BadRequest(w, "invalid request body: "+err.Error())
		return
	}
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httpx.Unauthorized(w, "unauthenticated")
		return
	}
	event, err := h.service.Create(r.Context(), req, userID)
	if err != nil {
		if errors.Is(err, ErrInvalidInput) {
			httpx.BadRequest(w, err.Error())
			return
		}
		httpx.Internal(w, "could not create event")
		return
	}
	httpx.Respond(w, http.StatusCreated, true, event, "")
}

// GetByID godoc
// GET /events/{id}
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	event, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			httpx.NotFound(w, "event not found")
			return
		}
		httpx.Internal(w, "could not fetch event")
		return
	}
	httpx.Respond(w, http.StatusOK, true, event, "")
}

// List godoc
// GET /events?status=open&page=1&limit=20
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	events, total, err := h.service.List(r.Context(), status, page, limit)
	if err != nil {
		httpx.Internal(w, "failed to list events")
		return
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	httpx.RespondMeta(w, http.StatusOK, events, &models.Meta{Total: total, Page: page, Limit: limit})
}

// Close godoc
// PUT /events/{id}/close  (admin)
func (h *Handler) Close(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	event, err := h.service.Close(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			httpx.NotFound(w, "event not found")
		case errors.Is(err, ErrInvalidTransition):
			httpx.Conflict(w, err.Error())
		default:
			httpx.Internal(w, "could not close event")
		}
		return
	}
	httpx.Respond(w, http.StatusOK, true, event, "")
}

// Cancel godoc
// PUT /events/{id}/cancel  (admin)
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}
	event, err := h.service.Cancel(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			httpx.NotFound(w, "event not found")
		case errors.Is(err, ErrInvalidTransition):
			httpx.Conflict(w, err.Error())
		default:
			httpx.Internal(w, "could not cancel event")
		}
		return
	}
	httpx.Respond(w, http.StatusOK, true, event, "")
}

func parseID(r *http.Request, key string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	if err != nil || id <= 0 {
		return 0, errInvalidID
	}
	return id, nil
}

var errInvalidID = errors.New("invalid id")
