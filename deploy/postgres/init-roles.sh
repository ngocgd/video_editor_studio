#!/bin/bash
# Runs once via docker-entrypoint-initdb.d on first init of the pgdata
# volume (as the bootstrap POSTGRES_USER superuser). Creates the
# least-privilege roles migrations, the API and the backup sidecar
# connect as, with passwords read from Docker secrets so they never
# appear in a committed file.
set -euo pipefail

OWNER_PASSWORD="$(cat /run/secrets/postgres_owner_password)"
APP_PASSWORD="$(cat /run/secrets/postgres_app_password)"
BACKUP_PASSWORD="$(cat /run/secrets/postgres_backup_password)"

# Passwords are passed as psql variables and substituted with :'var'
# (psql's own SQL-literal quoting) in plain top-level statements, not
# shell-interpolated directly into the SQL text: a literal single quote in
# an operator-chosen secret would otherwise break or inject into the
# CREATE/ALTER ROLE statements. psql's :'var' substitution is skipped
# inside a dollar-quoted (DO $$ ... $$) block, so the create-or-alter
# choice is made with \if/\gset instead of a PL/pgSQL DO block, keeping
# every CREATE/ALTER ROLE statement itself at the top level where
# substitution works.
psql -v ON_ERROR_STOP=1 \
    -v owner_password="$OWNER_PASSWORD" \
    -v app_password="$APP_PASSWORD" \
    -v backup_password="$BACKUP_PASSWORD" \
    -v db_name="$POSTGRES_DB" \
    --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-'EOSQL'
    SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_owner') AS exists \gset owner_
    \if :owner_exists
        ALTER ROLE loomtale_owner PASSWORD :'owner_password';
    \else
        CREATE ROLE loomtale_owner LOGIN PASSWORD :'owner_password';
    \endif

    SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_app') AS exists \gset app_
    \if :app_exists
        ALTER ROLE loomtale_app PASSWORD :'app_password';
    \else
        CREATE ROLE loomtale_app LOGIN PASSWORD :'app_password' NOSUPERUSER NOCREATEDB NOCREATEROLE;
    \endif

    SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_backup') AS exists \gset backup_
    \if :backup_exists
        ALTER ROLE loomtale_backup PASSWORD :'backup_password';
    \else
        CREATE ROLE loomtale_backup LOGIN PASSWORD :'backup_password' NOSUPERUSER NOCREATEDB NOCREATEROLE;
    \endif

    ALTER DATABASE :"db_name" OWNER TO loomtale_owner;
EOSQL
