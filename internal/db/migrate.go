package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies all *.sql files in the embedded migrations folder
// in numeric order, skipping any that have already been recorded in
// schema_migrations. Designed to be safe to invoke on every startup.
//
// File naming: "NNN_name.sql" — the leading number is the version.
// Each file is executed inside a transaction, so partial application
// is impossible.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// 1. Ensure the ledger exists even before any user migrations run.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		    version     BIGINT  PRIMARY KEY,
		    name        TEXT    NOT NULL,
		    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	// 2. Discover and sort migration files.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("reading embedded migrations: %w", err)
	}

	type migration struct {
		version int64
		name    string
		path    string
	}

	var migrations []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, name, err := parseMigrationFilename(e.Name())
		if err != nil {
			return err
		}
		migrations = append(migrations, migration{version: v, name: name, path: "migrations/" + e.Name()})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })

	// 3. Apply each migration that isn't yet recorded.
	for _, m := range migrations {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, m.version,
		).Scan(&exists); err != nil {
			return fmt.Errorf("checking migration %d: %w", m.version, err)
		}
		if exists {
			continue
		}

		body, err := fs.ReadFile(migrationsFS, m.path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", m.path, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("applying migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)
			 ON CONFLICT (version) DO NOTHING`,
			m.version, m.name,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("recording migration %d: %w", m.version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}

		log.Info().Int64("version", m.version).Str("name", m.name).Msg("applied migration")
	}

	return nil
}

func parseMigrationFilename(name string) (int64, string, error) {
	// Expect "<number>_<name>.sql"
	base := strings.TrimSuffix(name, ".sql")
	idx := strings.IndexRune(base, '_')
	if idx <= 0 {
		return 0, "", fmt.Errorf("migration filename must be NNN_name.sql, got %q", name)
	}
	v, err := strconv.ParseInt(base[:idx], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("migration %q: invalid version: %w", name, err)
	}
	return v, base[idx+1:], nil
}
