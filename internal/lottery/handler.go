package lottery

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Natthyx/lottery-system/internal/httpx"
)

// Handler wires lottery routes to the Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Draw godoc
// POST /events/{eventID}/draw  (admin)
func (h *Handler) Draw(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil || eventID <= 0 {
		httpx.BadRequest(w, "invalid event id")
		return
	}
	result, err := h.service.RunDraw(r.Context(), eventID)
	if err != nil {
		switch {
		case errors.Is(err, ErrEventNotFound):
			httpx.NotFound(w, err.Error())
		case errors.Is(err, ErrEventAlreadyDrawn),
			errors.Is(err, ErrEventCancelled),
			errors.Is(err, ErrEventNotClosed),
			errors.Is(err, ErrNoParticipants):
			httpx.Conflict(w, err.Error())
		default:
			httpx.Internal(w, "could not run draw")
		}
		return
	}
	httpx.Respond(w, http.StatusOK, true, result, "")
}

// Results godoc
// GET /events/{eventID}/results
func (h *Handler) Results(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(chi.URLParam(r, "eventID"), 10, 64)
	if err != nil || eventID <= 0 {
		httpx.BadRequest(w, "invalid event id")
		return
	}
	result, err := h.service.GetResults(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, ErrResultsUnavailable) {
			httpx.NotFound(w, err.Error())
			return
		}
		httpx.Internal(w, "could not fetch results")
		return
	}
	httpx.Respond(w, http.StatusOK, true, result, "")
}
