-- L6: audit-log tamper test. Every attempt runs inside a transaction that is
-- rolled back, so the audit rows are left exactly as they were. Run against a
-- database that already has at least one drawn event (run.sh does this).
--
-- Psql is run WITHOUT ON_ERROR_STOP for this file, because some statements
-- are expected to fail; each failure is part of the result.
\set ON_ERROR_STOP 0
\set VERBOSITY verbose

SELECT 'baseline' AS step, count(*) AS rows, md5(string_agg(id || ':' || winner_user_id || ':' || draw_rank, ',' ORDER BY id)) AS digest
FROM lottery_draws;

-- ── A. As the app role (the role in DATABASE_URL) ────────────────────────
SELECT 'A: as app role' AS step, current_user;

BEGIN;
UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK;

BEGIN;
DELETE FROM lottery_draws WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK;

BEGIN;
TRUNCATE lottery_draws;
ROLLBACK;

-- Real tamper (not a no-op): swap the rank-1 winner with the last-ranked
-- entrant for one event, then show the change is visible inside the tx.
BEGIN;
WITH ev AS (SELECT min(event_id) AS event_id FROM lottery_draws),
     r1 AS (SELECT d.id, d.winner_user_id FROM lottery_draws d, ev WHERE d.event_id = ev.event_id AND d.draw_rank = 1),
     rn AS (SELECT d.id, d.winner_user_id FROM lottery_draws d, ev WHERE d.event_id = ev.event_id
            ORDER BY d.draw_rank DESC LIMIT 1)
UPDATE lottery_draws t
SET winner_user_id = CASE WHEN t.id = r1.id THEN rn.winner_user_id ELSE r1.winner_user_id END
FROM r1, rn
WHERE t.id IN (r1.id, rn.id);
SELECT 'A: rank-1 winner after swap (inside tx)' AS step, event_id, draw_rank, winner_user_id
FROM lottery_draws WHERE event_id = (SELECT min(event_id) FROM lottery_draws) AND draw_rank = 1;
ROLLBACK;

-- ── B. As a non-superuser that OWNS the table ────────────────────────────
-- The app role is both superuser and owner, so superuser bypass and owner
-- privileges are indistinguishable for it. This isolates "owner" from
-- "superuser". Role and ownership change are rolled back.
BEGIN;
CREATE ROLE lt_tmp_owner NOLOGIN NOSUPERUSER;
ALTER TABLE lottery_draws OWNER TO lt_tmp_owner;
SET LOCAL ROLE lt_tmp_owner;
SELECT 'B: as non-superuser owner' AS step, current_user, (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) AS is_super;
SAVEPOINT s1;
UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s1;
DELETE FROM lottery_draws WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s1;
ROLLBACK;

-- ── C. As a non-superuser, non-owner role with only SELECT/INSERT ────────
-- This is what the REVOKE ... FROM PUBLIC in the migration would matter for.
BEGIN;
CREATE ROLE lt_tmp_app NOLOGIN NOSUPERUSER;
GRANT SELECT, INSERT ON lottery_draws TO lt_tmp_app;
SET LOCAL ROLE lt_tmp_app;
SELECT 'C: as non-owner with SELECT,INSERT' AS step, current_user;
SAVEPOINT s1;
UPDATE lottery_draws SET winner_user_id = winner_user_id WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s1;
DELETE FROM lottery_draws WHERE id = (SELECT min(id) FROM lottery_draws);
ROLLBACK TO SAVEPOINT s1;
ROLLBACK;

-- ── D. Is the REVOKE FROM PUBLIC doing anything? ─────────────────────────
-- PostgreSQL never grants UPDATE/DELETE on a table to PUBLIC by default, so
-- the REVOKE has nothing to remove. Its only visible effect is that relacl
-- becomes an explicit copy of the owner's default privileges, with no
-- PUBLIC entry, which is exactly what it would have been anyway.
SELECT 'D: acl' AS step, relacl FROM pg_class WHERE relname = 'lottery_draws';

-- ── E. Control: same as C on a scratch table that never had the REVOKE ───
-- If UPDATE/DELETE are denied here too, the denial in C comes from
-- PostgreSQL's default privileges, not from the migration's REVOKE.
BEGIN;
CREATE TABLE lt_tmp_control (id int);
INSERT INTO lt_tmp_control VALUES (1);
CREATE ROLE lt_tmp_app2 NOLOGIN NOSUPERUSER;
GRANT SELECT, INSERT ON lt_tmp_control TO lt_tmp_app2;
SET LOCAL ROLE lt_tmp_app2;
SELECT 'E: control table without REVOKE, non-owner with SELECT,INSERT' AS step, current_user;
SAVEPOINT s1;
UPDATE lt_tmp_control SET id = 2;
ROLLBACK TO SAVEPOINT s1;
DELETE FROM lt_tmp_control;
ROLLBACK TO SAVEPOINT s1;
ROLLBACK;

SELECT 'after' AS step, count(*) AS rows, md5(string_agg(id || ':' || winner_user_id || ':' || draw_rank, ',' ORDER BY id)) AS digest
FROM lottery_draws;
