#!/bin/sh
# Runs once, when the Postgres data volume is first initialised (official
# image: /docker-entrypoint-initdb.d). Creates the least-privilege role the
# app connects as at runtime. Table privileges are granted by migration 002,
# which runs as the schema owner (POSTGRES_USER).
#
# Existing volumes created before this script existed will not run it;
# create the role by hand (same statements) or recreate the volume.
set -eu
: "${APP_DB_PASSWORD:?APP_DB_PASSWORD must be set for the postgres service}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v app_password="$APP_DB_PASSWORD" -v db="$POSTGRES_DB" <<'EOSQL'
CREATE ROLE lottery_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS
    PASSWORD :'app_password';
REVOKE ALL ON DATABASE :"db" FROM PUBLIC;
GRANT CONNECT ON DATABASE :"db" TO lottery_app;
EOSQL
