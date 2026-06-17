package db

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// NewRedis builds a *redis.Client from pre-parsed options and verifies
// connectivity. Returning the error (instead of falling through) lets
// the bootstrap fail fast on misconfiguration.
func NewRedis(ctx context.Context, opts *redis.Options) (*redis.Client, error) {
	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("pinging Redis: %w", err)
	}

	log.Info().Str("addr", opts.Addr).Int("db", opts.DB).Msg("Redis connection established")
	return client, nil
}
