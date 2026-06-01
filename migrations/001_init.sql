-- ============================================================
-- Lottery System Database Schema
-- Production-grade with audit trail, constraints, and indexes
-- ============================================================

-- Enable UUID extension (useful for future API-facing IDs)
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ---------------------------------------------------------------
-- USERS
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    full_name     TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------
-- EVENTS
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS events (
    id           BIGSERIAL PRIMARY KEY,
    title        TEXT NOT NULL,
    description  TEXT,
    capacity     INT NOT NULL CHECK (capacity > 0),
    draw_at      TIMESTAMPTZ NOT NULL,             -- scheduled lottery draw time
    status       TEXT NOT NULL DEFAULT 'open'
                 CHECK (status IN ('open', 'closed', 'drawn', 'cancelled')),
    created_by   BIGINT NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_events_status    ON events(status);
CREATE INDEX idx_events_draw_at   ON events(draw_at);

-- ---------------------------------------------------------------
-- BOOKINGS
-- User books an event → becomes eligible for the lottery draw
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bookings (
    id          BIGSERIAL PRIMARY KEY,
    event_id    BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id),
    booked_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (event_id, user_id)                     -- one booking per user per event
);

CREATE INDEX idx_bookings_event_id ON bookings(event_id);
CREATE INDEX idx_bookings_user_id  ON bookings(user_id);

-- ---------------------------------------------------------------
-- LOTTERY DRAWS
-- Append-only audit log — never updated after insert.
-- Records who won, their rank, and the entropy source used.
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS lottery_draws (
    id              BIGSERIAL PRIMARY KEY,
    event_id        BIGINT NOT NULL REFERENCES events(id),
    winner_user_id  BIGINT NOT NULL REFERENCES users(id),
    draw_rank       INT NOT NULL CHECK (draw_rank > 0),  -- 1 = winner, 2+ = waitlist
    total_entrants  INT NOT NULL,                        -- snapshot of pool size at draw time
    entropy_source  TEXT NOT NULL DEFAULT 'crypto/rand+fisher-yates',
    drawn_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (event_id, draw_rank)                         -- one winner per rank per event
);

CREATE INDEX idx_draws_event_id ON lottery_draws(event_id);

-- ---------------------------------------------------------------
-- Prevent UPDATE/DELETE on lottery_draws (tamper-evident)
-- In production you'd also use row-level security and a
-- separate read-only audit role.
-- ---------------------------------------------------------------
CREATE OR REPLACE RULE no_update_draws AS
    ON UPDATE TO lottery_draws DO INSTEAD NOTHING;

CREATE OR REPLACE RULE no_delete_draws AS
    ON DELETE TO lottery_draws DO INSTEAD NOTHING;

-- ---------------------------------------------------------------
-- updated_at auto-trigger
-- ---------------------------------------------------------------
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_events_updated_at
    BEFORE UPDATE ON events
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------
-- Seed: default admin user
-- Password: Admin1234!  (bcrypt hash — change in production)
-- ---------------------------------------------------------------
INSERT INTO users (email, password_hash, full_name, role)
VALUES (
    'admin@lottery.dev',
    '$2a$12$KIXbFMbXl0S5LPCLgKlRwukfJZe6W3y8P3AUCB0B5zWKa0fJpBCAe',
    'System Admin',
    'admin'
) ON CONFLICT DO NOTHING;
