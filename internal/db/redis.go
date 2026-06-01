package db

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// NewRedis creates and validates a Redis client connection.
//
// Redis serves two purposes in this system:
//  1. Distributed locks  — preventing race conditions on booking
//  2. Capacity counters  — fast atomic INCR/DECR without touching Postgres
//
// WHY Redis locks over Postgres advisory locks:
//   Redis SETNX is atomic and auto-expires via TTL, so a crashed process
//   never leaves a lock dangling permanently.
func NewRedis(ctx context.Context, addr, password string, dbIndex int) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           dbIndex,
		PoolSize:     20,
		MinIdleConns: 5,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("pinging Redis: %w", err)
	}

	log.Info().Str("addr", addr).Msg("Redis connection established")
	return client, nil
}
