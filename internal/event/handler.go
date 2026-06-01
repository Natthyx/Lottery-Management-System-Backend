package event

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/natannan/lottery-system/internal/middleware"
	"github.com/natannan/lottery-system/internal/models"
)

// Handler wires HTTP routes to the event Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Routes registers all event routes onto a chi router.
// Called from main.go during server setup.
func (h *Handler) Routes() func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/", h.List)
		r.Get("/{id}", h.GetByID)

		// Admin-only routes
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAdmin)
			r.Post("/", h.Create)
		})
	}
}

// Create godoc
// POST /events
// Body: CreateEventRequest JSON
// Auth: admin JWT required
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond(w, http.StatusBadRequest, false, nil, "invalid request body")
		return
	}

	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		respond(w, http.StatusUnauthorized, false, nil, "unauthenticated")
		return
	}

	event, err := h.service.Create(r.Context(), req, userID)
	if err != nil {
		respond(w, http.StatusBadRequest, false, nil, err.Error())
		return
	}

	respond(w, http.StatusCreated, true, event, "")
}

// GetByID godoc
// GET /events/{id}
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		respond(w, http.StatusBadRequest, false, nil, "invalid event id")
		return
	}

	event, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		respond(w, http.StatusNotFound, false, nil, err.Error())
		return
	}

	respond(w, http.StatusOK, true, event, "")
}

// List godoc
// GET /events?status=open&page=1&limit=20
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	events, total, err := h.service.List(r.Context(), status, page, limit)
	if err != nil {
		respond(w, http.StatusInternalServerError, false, nil, "failed to list events")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(models.APIResponse{ //nolint:errcheck
		Success: true,
		Data:    events,
		Meta: &models.Meta{
			Total: total,
			Page:  page,
			Limit: limit,
		},
	})
}

// ── shared helper ─────────────────────────────────────────────────────────────

func respond(w http.ResponseWriter, status int, success bool, data interface{}, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(models.APIResponse{ //nolint:errcheck
		Success: success,
		Data:    data,
		Error:   errMsg,
	})
}
