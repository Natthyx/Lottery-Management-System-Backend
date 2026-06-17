-- ============================================================
-- Lottery System — Initial Schema
-- Idempotent. Safe to re-run.
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ── USERS ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    full_name     TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);

-- ── EVENTS ───────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS events (
    id            BIGSERIAL PRIMARY KEY,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    capacity      INT  NOT NULL CHECK (capacity > 0),
    winner_count  INT  NOT NULL DEFAULT 1 CHECK (winner_count > 0),
    draw_at       TIMESTAMPTZ NOT NULL,
    status        TEXT NOT NULL DEFAULT 'open'
                  CHECK (status IN ('open', 'closed', 'drawn', 'cancelled')),
    created_by    BIGINT NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT events_winner_count_lte_capacity CHECK (winner_count <= capacity)
);

CREATE INDEX IF NOT EXISTS idx_events_status  ON events(status);
CREATE INDEX IF NOT EXISTS idx_events_draw_at ON events(draw_at);

-- ── BOOKINGS ─────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS bookings (
    id        BIGSERIAL PRIMARY KEY,
    event_id  BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id   BIGINT NOT NULL REFERENCES users(id),
    booked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (event_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_bookings_event_id ON bookings(event_id);
CREATE INDEX IF NOT EXISTS idx_bookings_user_id  ON bookings(user_id);

-- ── LOTTERY DRAWS (append-only audit log) ────────────────────
CREATE TABLE IF NOT EXISTS lottery_draws (
    id              BIGSERIAL PRIMARY KEY,
    event_id        BIGINT NOT NULL REFERENCES events(id),
    winner_user_id  BIGINT NOT NULL REFERENCES users(id),
    draw_rank       INT NOT NULL CHECK (draw_rank > 0),
    total_entrants  INT NOT NULL,
    entropy_source  TEXT NOT NULL DEFAULT 'crypto/rand+fisher-yates',
    drawn_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (event_id, draw_rank)
);

CREATE INDEX IF NOT EXISTS idx_draws_event_id ON lottery_draws(event_id);

-- Tamper-evidence: revoke UPDATE/DELETE from PUBLIC so any
-- attempt raises a real permission error (not a silent no-op).
REVOKE UPDATE, DELETE ON lottery_draws FROM PUBLIC;

-- ── updated_at trigger ───────────────────────────────────────
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_users_updated_at  ON users;
DROP TRIGGER IF EXISTS trg_events_updated_at ON events;

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_events_updated_at
    BEFORE UPDATE ON events
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── Schema migration ledger ──────────────────────────────────
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     BIGINT  PRIMARY KEY,
    name        TEXT    NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version, name)
VALUES (1, 'init')
ON CONFLICT (version) DO NOTHING;
