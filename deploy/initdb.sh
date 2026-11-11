#!/bin/sh
set -eu
psql -v ON_ERROR_STOP=1 -f /quickstack/roles.sql
psql -v ON_ERROR_STOP=1 \
	-v owner_pw="$QUICKSTACK_OWNER_PASSWORD" \
	-v app_pw="$QUICKSTACK_APP_PASSWORD" <<'SQL'
ALTER ROLE quickstack_owner PASSWORD :'owner_pw';
ALTER ROLE quickstack_app PASSWORD :'app_pw';
ALTER DATABASE quickstack OWNER TO quickstack_owner;
SQL
