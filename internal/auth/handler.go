package auth

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Natthyx/lottery-system/internal/httpx"
	"github.com/Natthyx/lottery-system/internal/middleware"
	"github.com/Natthyx/lottery-system/internal/models"
)

// Handler wires auth routes to the Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// Register godoc
// POST /auth/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	user, err := h.service.Register(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailTaken):
			httpx.Conflict(w, err.Error())
		case errors.Is(err, ErrInvalidInput):
			httpx.BadRequest(w, err.Error())
		default:
			httpx.Internal(w, "could not register user")
		}
		return
	}
	httpx.Respond(w, http.StatusCreated, true, user, "")
}

// Login godoc
// POST /auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.BadRequest(w, "invalid request body: "+err.Error())
		return
	}

	resp, err := h.service.Login(r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.Unauthorized(w, err.Error())
			return
		}
		httpx.Internal(w, "could not authenticate")
		return
	}
	httpx.Respond(w, http.StatusOK, true, resp, "")
}

// Me godoc
// GET /auth/me  (authenticated)
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserID(r.Context())
	if !ok {
		httpx.Unauthorized(w, "unauthenticated")
		return
	}
	user, err := h.service.Me(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httpx.NotFound(w, "user not found")
			return
		}
		httpx.Internal(w, "could not fetch user")
		return
	}
	httpx.Respond(w, http.StatusOK, true, user, "")
}

// PromoteToAdmin godoc
// POST /admin/users/{id}/promote  (admin only)
func (h *Handler) PromoteToAdmin(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		httpx.BadRequest(w, "invalid user id")
		return
	}
	user, err := h.service.PromoteToAdmin(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httpx.NotFound(w, "user not found")
			return
		}
		httpx.Internal(w, "could not promote user")
		return
	}
	httpx.Respond(w, http.StatusOK, true, user, "")
}
