package auth

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/natannan/lottery-system/internal/models"
)

// Handler wires auth routes to the Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Routes() func(chi.Router) {
	return func(r chi.Router) {
		r.Post("/register", h.Register)
		r.Post("/login", h.Login)
	}
}

// Register godoc
// POST /auth/register
// Body: { "email": "...", "password": "...", "full_name": "..." }
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond(w, http.StatusBadRequest, false, nil, "invalid request body")
		return
	}

	user, err := h.service.Register(r.Context(), req)
	if err != nil {
		respond(w, http.StatusBadRequest, false, nil, err.Error())
		return
	}

	respond(w, http.StatusCreated, true, user, "")
}

// Login godoc
// POST /auth/login
// Body: { "email": "...", "password": "..." }
// Returns: { "token": "...", "user": { ... } }
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond(w, http.StatusBadRequest, false, nil, "invalid request body")
		return
	}

	resp, err := h.service.Login(r.Context(), req)
	if err != nil {
		respond(w, http.StatusUnauthorized, false, nil, err.Error())
		return
	}

	respond(w, http.StatusOK, true, resp, "")
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
