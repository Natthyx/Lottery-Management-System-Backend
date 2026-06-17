package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/Natthyx/lottery-system/internal/models"
)

// Respond writes a uniform JSON envelope. All handlers must use this.
func Respond(w http.ResponseWriter, status int, success bool, data interface{}, errMsg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(models.APIResponse{
		Success: success,
		Data:    data,
		Error:   errMsg,
	}); err != nil {
		log.Warn().Err(err).Msg("writing JSON response")
	}
}

// RespondMeta is the list variant carrying pagination metadata.
func RespondMeta(w http.ResponseWriter, status int, data interface{}, meta *models.Meta) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(models.APIResponse{
		Success: true,
		Data:    data,
		Meta:    meta,
	}); err != nil {
		log.Warn().Err(err).Msg("writing JSON response")
	}
}

// DecodeJSON decodes a request body into v, returning a 400-suitable error
// on failure. It rejects unknown fields to surface client typos.
//
// Body size is bounded by the BodyLimit middleware before reaching here,
// so we don't need a second MaxBytesReader.
func DecodeJSON(r *http.Request, v interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		return err
	}
	// Reject extra JSON after the first value (e.g. "{} {}").
	if dec.More() {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

// BadRequest, Unauthorized, Forbidden, NotFound, Conflict, Internal are
// thin wrappers that keep handler code readable.
func BadRequest(w http.ResponseWriter, msg string) {
	Respond(w, http.StatusBadRequest, false, nil, msg)
}
func Unauthorized(w http.ResponseWriter, msg string) {
	Respond(w, http.StatusUnauthorized, false, nil, msg)
}
func Forbidden(w http.ResponseWriter, msg string) { Respond(w, http.StatusForbidden, false, nil, msg) }
func NotFound(w http.ResponseWriter, msg string)  { Respond(w, http.StatusNotFound, false, nil, msg) }
func Conflict(w http.ResponseWriter, msg string)  { Respond(w, http.StatusConflict, false, nil, msg) }
func Internal(w http.ResponseWriter, msg string) {
	Respond(w, http.StatusInternalServerError, false, nil, msg)
}
