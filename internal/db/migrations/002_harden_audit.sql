-- ============================================================
-- 002: make the draw audit trail append-only and tamper-evident,
-- and give the runtime role only the privileges it needs.
-- Idempotent. Safe to re-run.
-- ============================================================

-- ── Append-only enforcement ───────────────────────────────────
-- The REVOKE ... FROM PUBLIC in 001 did nothing (PUBLIC never had UPDATE or
-- DELETE). These triggers fire for every role, including the table owner
-- and superusers. (A superuser can still disable triggers or set
-- session_replication_role = replica; the draw seals below are what make
-- that detectable.)
CREATE OR REPLACE FUNCTION forbid_audit_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% on % is not allowed: the lottery audit trail is append-only', TG_OP, TG_TABLE_NAME
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_lottery_draws_append_only ON lottery_draws;
CREATE TRIGGER trg_lottery_draws_append_only
    BEFORE UPDATE OR DELETE ON lottery_draws
    FOR EACH ROW EXECUTE FUNCTION forbid_audit_mutation();

DROP TRIGGER IF EXISTS trg_lottery_draws_no_truncate ON lottery_draws;
CREATE TRIGGER trg_lottery_draws_no_truncate
    BEFORE TRUNCATE ON lottery_draws
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_audit_mutation();

-- ── Draw seals ────────────────────────────────────────────────
-- One SHA-256 digest per draw over the full ordered outcome (see
-- internal/lottery/seal.go). GET /events/{id}/results recomputes it.
CREATE TABLE IF NOT EXISTS lottery_draw_seals (
    event_id   BIGINT PRIMARY KEY REFERENCES events(id),
    digest     TEXT NOT NULL,
    algorithm  TEXT NOT NULL,
    sealed_at  TIMESTAMPTZ NOT NULL
);

DROP TRIGGER IF EXISTS trg_lottery_draw_seals_append_only ON lottery_draw_seals;
CREATE TRIGGER trg_lottery_draw_seals_append_only
    BEFORE UPDATE OR DELETE ON lottery_draw_seals
    FOR EACH ROW EXECUTE FUNCTION forbid_audit_mutation();

DROP TRIGGER IF EXISTS trg_lottery_draw_seals_no_truncate ON lottery_draw_seals;
CREATE TRIGGER trg_lottery_draw_seals_no_truncate
    BEFORE TRUNCATE ON lottery_draw_seals
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_audit_mutation();

-- ── A drawn event is final ────────────────────────────────────
-- Results are split into winners/waitlist by events.winner_count, so once an
-- event is drawn neither its status nor the numbers that shape the result
-- may change.
CREATE OR REPLACE FUNCTION forbid_drawn_event_change()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.status = 'drawn' THEN
            RAISE EXCEPTION 'event % has been drawn and cannot be deleted', OLD.id
                USING ERRCODE = 'insufficient_privilege';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.status = 'drawn' AND (NEW.status, NEW.winner_count, NEW.capacity)
                                IS DISTINCT FROM (OLD.status, OLD.winner_count, OLD.capacity) THEN
        RAISE EXCEPTION 'event % has been drawn; status, winner_count and capacity are final', OLD.id
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_events_drawn_is_final ON events;
CREATE TRIGGER trg_events_drawn_is_final
    BEFORE UPDATE OR DELETE ON events
    FOR EACH ROW EXECUTE FUNCTION forbid_drawn_event_change();

-- ── Least-privilege runtime role ──────────────────────────────
-- The app should connect as "lottery_app" (created outside migrations, see
-- deploy/postgres/initdb/01-app-role.sh) and run migrations as the schema
-- owner via MIGRATION_DATABASE_URL. The runtime role can read and append to
-- the audit tables but has no UPDATE, DELETE or TRUNCATE on them, and owns
-- nothing, so it cannot drop or disable the triggers above.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'lottery_app') THEN
        REVOKE ALL ON ALL TABLES IN SCHEMA public FROM lottery_app;
        REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM lottery_app;
        GRANT USAGE ON SCHEMA public TO lottery_app;
        GRANT SELECT, INSERT, UPDATE ON users, events TO lottery_app;
        GRANT SELECT, INSERT ON bookings, lottery_draws, lottery_draw_seals TO lottery_app;
        GRANT SELECT ON schema_migrations TO lottery_app;
        GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO lottery_app;
    END IF;
END
$$;
