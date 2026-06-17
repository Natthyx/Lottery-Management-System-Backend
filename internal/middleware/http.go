package middleware

import (
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"

	"github.com/Natthyx/lottery-system/internal/httpx"
)

// responseWriter captures the HTTP status code so the logger middleware
// can record it. We intentionally avoid the underlying writer's WriteHeader
// being called more than once.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	bytes       int
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(p []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(p)
	rw.bytes += n
	return n, err
}

// Logger emits a structured JSON line for every request, including the
// chi request ID, latency, byte count and remote IP. Skips /health to
// reduce noise; bump verbosity by removing the check.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if r.URL.Path == "/health" || r.URL.Path == "/ready" {
				return
			}
			log.Info().
				Str("request_id", chiMiddleware.GetReqID(r.Context())).
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", wrapped.status).
				Int("bytes", wrapped.bytes).
				Dur("latency", time.Since(start)).
				Str("remote_addr", r.RemoteAddr).
				Str("user_agent", r.UserAgent()).
				Msg("http_request")
		}()
		next.ServeHTTP(wrapped, r)
	})
}

// Recoverer catches panics, logs them with the request ID and a stack
// trace, and returns a 500 JSON envelope instead of crashing the process.
// Place this OUTSIDE of Logger so the panic is still recorded by Logger.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil && rec != http.ErrAbortHandler {
				log.Error().
					Str("request_id", chiMiddleware.GetReqID(r.Context())).
					Interface("panic", rec).
					Bytes("stack", debug.Stack()).
					Msg("recovered from panic")
				httpx.Internal(w, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// BodyLimit rejects any request whose body exceeds maxBytes with 413.
// Wraps r.Body in http.MaxBytesReader so subsequent decoders fail at
// the correct boundary.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS returns a permissive-but-configurable CORS middleware. If
// allowedOrigins is empty, CORS is disabled (no headers added).
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowAll := len(allowedOrigins) == 1 && allowedOrigins[0] == "*"
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[strings.ToLower(o)] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && len(allowedOrigins) > 0 {
				_, ok := allowed[strings.ToLower(origin)]
				if allowAll || ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
					w.Header().Set("Access-Control-Max-Age", "600")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
