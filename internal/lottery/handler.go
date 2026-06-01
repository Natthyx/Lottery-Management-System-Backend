package lottery

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/natannan/lottery-system/internal/models"
)

// Handler wires lottery routes to the Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Routes() func(chi.Router) {
	return func(r chi.Router) {
		// POST /events/{eventID}/draw  — trigger the lottery (admin only, enforced in main.go)
		r.Post("/events/{eventID}/draw", h.Draw)

		// GET /events/{eventID}/results — fetch draw results (public)
		r.Get("/events/{eventID}/results", h.Results)
	}
}

// Draw godoc
// POST /events/{eventID}/draw
// Triggers the lottery algorithm for the given event.
// Admin only. Idempotent attempt: if already drawn, returns 409.
func (h *Handler) Draw(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil {
		respond(w, http.StatusBadRequest, false, nil, "invalid event id")
		return
	}

	result, err := h.service.RunDraw(r.Context(), eventID)
	if err != nil {
		// Could be "already drawn", "no participants", "event cancelled" etc.
		respond(w, http.StatusConflict, false, nil, err.Error())
		return
	}

	respond(w, http.StatusOK, true, result, "")
}

// Results godoc
// GET /events/{eventID}/results
// Returns the stored lottery draw results. Public endpoint.
func (h *Handler) Results(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil {
		respond(w, http.StatusBadRequest, false, nil, "invalid event id")
		return
	}

	result, err := h.service.GetResults(r.Context(), eventID)
	if err != nil {
		respond(w, http.StatusNotFound, false, nil, err.Error())
		return
	}

	respond(w, http.StatusOK, true, result, "")
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
