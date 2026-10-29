-- Phase 0 / L6: who does the app connect as, who owns the audit tables,
-- and what protects them. Read-only. Run as the schema owner (psql -U lottery).

-- Roles: the owner used for migrations and the runtime role the app uses.
SELECT rolname, rolsuper, rolbypassrls, rolcreaterole, rolcreatedb, rolcanlogin
FROM pg_roles WHERE rolname IN ('lottery', 'lottery_app') ORDER BY rolname;

-- Which role the running app is actually connected as.
SELECT usename, count(*) AS connections
FROM pg_stat_activity WHERE datname = current_database() AND backend_type = 'client backend'
GROUP BY usename ORDER BY usename;

SELECT c.relname, pg_get_userbyid(c.relowner) AS owner, c.relacl
FROM pg_class c
WHERE c.relname IN ('lottery_draws', 'lottery_draw_seals', 'events', 'bookings', 'users')
ORDER BY c.relname;

-- Privileges of the runtime role on each table.
SELECT t AS table_name,
  has_table_privilege('lottery_app', t, 'SELECT')   AS sel,
  has_table_privilege('lottery_app', t, 'INSERT')   AS ins,
  has_table_privilege('lottery_app', t, 'UPDATE')   AS upd,
  has_table_privilege('lottery_app', t, 'DELETE')   AS del,
  has_table_privilege('lottery_app', t, 'TRUNCATE') AS trunc
FROM unnest(ARRAY['users', 'events', 'bookings', 'lottery_draws', 'lottery_draw_seals', 'schema_migrations']) AS t
ORDER BY t;

-- Triggers protecting the audit trail and drawn events.
SELECT tgrelid::regclass AS table_name, tgname, tgenabled, pg_get_triggerdef(oid) AS definition
FROM pg_trigger
WHERE tgrelid IN ('lottery_draws'::regclass, 'lottery_draw_seals'::regclass, 'events'::regclass) AND NOT tgisinternal
ORDER BY 1, 2;

SELECT version, name, applied_at FROM schema_migrations ORDER BY version;
