package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// livenessHandler is a cheap "process is up" probe. It must not depend
// on Postgres or Redis — if those are temporarily unavailable, Kubernetes
// should not kill the pod (that's what readiness is for).
func livenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
		})
	}
}

// readinessHandler verifies that Postgres and Redis are reachable. Used
// by load balancers to decide whether to route traffic to this replica.
func readinessHandler(pool *pgxpool.Pool, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		checks := map[string]string{
			"postgres": "ok",
			"redis":    "ok",
		}
		status := http.StatusOK

		if err := pool.Ping(ctx); err != nil {
			checks["postgres"] = "down: " + err.Error()
			status = http.StatusServiceUnavailable
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			checks["redis"] = "down: " + err.Error()
			status = http.StatusServiceUnavailable
		}

		writeJSON(w, status, map[string]any{
			"status": map[bool]string{true: "ok", false: "degraded"}[status == http.StatusOK],
			"checks": checks,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
