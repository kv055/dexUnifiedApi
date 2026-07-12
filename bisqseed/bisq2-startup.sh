#!/bin/sh
set -eu

APP_DIR=/home/appuser/.local/share/Bisq2
JAVA_BIN=/opt/java/openjdk/bin/java
BACKUP_DATABASE_URL="${BISQ2_BACKUP_DATABASE_URL:-${BISQ2_SEED_BACKUP_DATABASE_URL:-}}"
ACCOUNT_PROVIDER="Bisq2"

mkdir -p "$APP_DIR"
chown -R appuser:appuser "$APP_DIR" 2>/dev/null || \
  echo "[startup] Could not chown every Bisq2 data file; continuing in case some files are mounted read-only" >&2

ensure_account_row_in_database() {
  if [ -z "$BACKUP_DATABASE_URL" ]; then
    return 0
  fi

  if ! psql \
    -v BISQ2_ACCOUNT_PROVIDER_SQL="$ACCOUNT_PROVIDER" \
    "$BACKUP_DATABASE_URL" <<'SQL'
\set ON_ERROR_STOP on
INSERT INTO accounts (provider, category)
SELECT :'BISQ2_ACCOUNT_PROVIDER_SQL', 'DEX'
WHERE NOT EXISTS (
    SELECT 1 FROM accounts WHERE provider = :'BISQ2_ACCOUNT_PROVIDER_SQL'
);
SQL
  then
    echo "[startup] Failed to ensure Bisq2 account row for provider '$ACCOUNT_PROVIDER'" >&2
    exit 1
  fi

  echo "[startup] Bisq2 account row ensured for provider '$ACCOUNT_PROVIDER'"
}

ensure_account_row_in_database

# Launch Bisq 2 API daemon.
#
# REST API is configured under application.api.server.* in api_app.conf
# (bundled inside api-app.jar). Overrides are passed as typesafe-config system
# properties (-D...) so they apply regardless of how the daemon maps CLI args.
# In v2.1.11 the shorter --api.bind.host=... CLI form was silently ignored and
# the bundled default of host="127.0.0.1" won, leaving the REST server bound to
# loopback only — unreachable from other containers despite api.bind.host=0.0.0.0
# being passed.
#
# If Bisq 2 is configured to use its own embedded Tor for the P2P layer (the default),
# it will try to start a Tor daemon inside the container. To route through the shared
# `tor` container instead, set the env var BISQ2_TOR_SOCKS_HOST and BISQ2_TOR_SOCKS_PORT
# and uncomment the two flags below (exact flag names TBD from Bisq 2 docs):
#   --networkServiceConfig.torSocksHost=${BISQ2_TOR_SOCKS_HOST:-tor}
#   --networkServiceConfig.torSocksPort=${BISQ2_TOR_SOCKS_PORT:-9050}

# authorizationRequired=false is deliberate: in v2.1.11 that flag enables the
# device-pairing clientId permission model (Headers.CLIENT_ID + AccessApi
# pairing), NOT a static password — our Go clients cannot satisfy it. The
# security boundary is the Docker bridge network plus the loopback-only 8090
# publish in docker-compose.yml; bind.host=0.0.0.0 below is container-internal.
su -s /bin/sh appuser -c "exec '$JAVA_BIN' \
  -Xmx2g \
  -XX:MaxMetaspaceSize=256m \
  -XX:ReservedCodeCacheSize=128m \
  -XX:MaxDirectMemorySize=128m \
  -XX:+UseG1GC \
  -Dapplication.api.server.restEnabled=true \
  -Dapplication.api.server.bind.host=0.0.0.0 \
  -Dapplication.api.server.bind.port=8090 \
  -Dapplication.api.server.security.authorizationRequired=false \
  -cp '/opt/bisq2/lib/*' \
  bisq.api_app.ApiApp \
  --data-dir='$APP_DIR'" &

BISQ2_PID=$!

cleanup() {
  if kill -0 "$BISQ2_PID" 2>/dev/null; then
    kill "$BISQ2_PID" 2>/dev/null || true
    wait "$BISQ2_PID" 2>/dev/null || true
  fi
}
trap cleanup INT TERM

wait "$BISQ2_PID"
