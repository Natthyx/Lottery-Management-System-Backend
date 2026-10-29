-- L6: audit-log tamper test (after the fix). Every attempt runs inside a
-- transaction that is rolled back, so the audit rows are left as they were.
-- Run as the superuser/owner (psql -U lottery) against a database that has
-- at least one drawn event. Expected-failure statements are part of the
-- result, so ON_ERROR_STOP is off.
\set ON_ERROR_STOP 0
\set VERBOSITY verbose

SELECT 'baseline' AS step, count(*) AS rows, md5(string_agg(id || ':' || winner_user_id || ':' || draw_rank, ',' ORDER BY id)) AS digest
FROM lottery_draws;

-- ── A. As the runtime role the app connects as (lottery_app) ──────────────
BEGIN;
SET LOCAL ROLE lottery_app;
SELECT 'A: as runtime role' AS step, current_user;
SAVEPOINT s;
UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s;
DELETE FROM lottery_draws WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s;
TRUNCATE lottery_draws;
ROLLBACK TO SAVEPOINT s;
DELETE FROM lottery_draw_seals;
ROLLBACK TO SAVEPOINT s;
ALTER TABLE lottery_draws DISABLE TRIGGER trg_lottery_draws_append_only;
ROLLBACK TO SAVEPOINT s;
DROP TRIGGER trg_lottery_draws_append_only ON lottery_draws;
ROLLBACK TO SAVEPOINT s;
SET LOCAL session_replication_role = replica;
ROLLBACK TO SAVEPOINT s;
UPDATE events SET status = 'closed' WHERE status = 'drawn';
ROLLBACK;

-- ── B. As the schema owner, which is also a superuser here (lottery) ──────
SELECT 'B: as owner/superuser' AS step, current_user;
BEGIN;
SAVEPOINT s;
UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s;
DELETE FROM lottery_draws WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s;
TRUNCATE lottery_draws;
ROLLBACK TO SAVEPOINT s;
UPDATE lottery_draw_seals SET digest = 'forged';
ROLLBACK TO SAVEPOINT s;
UPDATE events SET winner_count = winner_count + 1 WHERE status = 'drawn';
ROLLBACK TO SAVEPOINT s;
UPDATE events SET status = 'closed' WHERE status = 'drawn';
ROLLBACK;

-- ── C. As a non-superuser owner ───────────────────────────────────────────
-- Triggers still fire for the owner, but an owner may disable them. That is
-- a residual risk of whoever holds the owner credentials (migrations only);
-- the draw seal is what detects it (see run.sh L6 tamper-detection step).
BEGIN;
CREATE ROLE lt_tmp_owner NOLOGIN NOSUPERUSER;
ALTER TABLE lottery_draws OWNER TO lt_tmp_owner;
SET LOCAL ROLE lt_tmp_owner;
SELECT 'C: as non-superuser owner' AS step, current_user;
SAVEPOINT s;
UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s;
ALTER TABLE lottery_draws DISABLE TRIGGER trg_lottery_draws_append_only;
UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK;

SELECT 'after' AS step, count(*) AS rows, md5(string_agg(id || ':' || winner_user_id || ':' || draw_rank, ',' ORDER BY id)) AS digest
FROM lottery_draws;
