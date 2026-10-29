-- Phase 0 / L6: who does the app connect as, who owns lottery_draws, and
-- what actually protects it. Read-only.

-- The role in DATABASE_URL (docker-compose.yml: POSTGRES_USER, default "lottery").
SELECT current_user, session_user;

SELECT rolname, rolsuper, rolbypassrls, rolcreaterole, rolcreatedb
FROM pg_roles WHERE rolname = current_user;

SELECT c.relname, pg_get_userbyid(c.relowner) AS owner, c.relacl
FROM pg_class c
WHERE c.relname = 'lottery_draws';

-- Privileges as reported by the catalog for the app role and for PUBLIC.
SELECT
  has_table_privilege(current_user, 'lottery_draws', 'UPDATE')   AS app_can_update,
  has_table_privilege(current_user, 'lottery_draws', 'DELETE')   AS app_can_delete,
  has_table_privilege(current_user, 'lottery_draws', 'TRUNCATE') AS app_can_truncate;

SELECT grantee, privilege_type
FROM information_schema.role_table_grants
WHERE table_name = 'lottery_draws'
ORDER BY grantee, privilege_type;

-- Any non-internal triggers on lottery_draws? (Expected by the spec to be checked.)
SELECT tgname, pg_get_triggerdef(oid) AS definition
FROM pg_trigger
WHERE tgrelid = 'lottery_draws'::regclass AND NOT tgisinternal;

-- Any rules or row-level security?
SELECT rulename FROM pg_rules WHERE tablename = 'lottery_draws';
SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname = 'lottery_draws';
