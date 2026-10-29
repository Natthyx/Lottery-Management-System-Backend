// Package integration holds tests that need a real PostgreSQL and Redis.
// They run when TEST_DATABASE_URL (a superuser / schema-owner DSN) and
// TEST_REDIS_URL are set, as they are in CI, and are skipped otherwise.
package integration
