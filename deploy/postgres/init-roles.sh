#!/bin/bash
# Runs once via docker-entrypoint-initdb.d on first init of the pgdata
# volume (as the bootstrap POSTGRES_USER superuser). Creates the two
# least-privilege roles migrations and the API connect as, with passwords
# read from Docker secrets so they never appear in a committed file.
set -euo pipefail

OWNER_PASSWORD="$(cat /run/secrets/postgres_owner_password)"
APP_PASSWORD="$(cat /run/secrets/postgres_app_password)"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    DO \$\$
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_owner') THEN
            CREATE ROLE loomtale_owner LOGIN PASSWORD '${OWNER_PASSWORD}';
        ELSE
            ALTER ROLE loomtale_owner PASSWORD '${OWNER_PASSWORD}';
        END IF;

        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'loomtale_app') THEN
            CREATE ROLE loomtale_app LOGIN PASSWORD '${APP_PASSWORD}' NOSUPERUSER NOCREATEDB NOCREATEROLE;
        ELSE
            ALTER ROLE loomtale_app PASSWORD '${APP_PASSWORD}';
        END IF;
    END
    \$\$;

    ALTER DATABASE "$POSTGRES_DB" OWNER TO loomtale_owner;
EOSQL
