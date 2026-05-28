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
INSERT INTO accounts (provider, email, password)
VALUES (:'BISQ2_ACCOUNT_PROVIDER_SQL', NULL, NULL)
ON CONFLICT (provider) DO NOTHING;
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
# Key flags:
#   --data-dir              where Bisq2 stores its state (wallets, P2P data, etc.)
#   --api.restEnabled=true  enable the REST API (disabled by default)
#   --api.bind.host         bind the REST server to all interfaces so unified-api can reach it
#   --api.bind.port         REST port (default 8090)
#   --api.security.authorizationRequired=false
#                           skip the device-pairing auth flow; safe inside a private Docker network
#
# If Bisq 2 is configured to use its own embedded Tor for the P2P layer (the default),
# it will try to start a Tor daemon inside the container. To route through the shared
# `tor` container instead, set the env var BISQ2_TOR_SOCKS_HOST and BISQ2_TOR_SOCKS_PORT
# and uncomment the two flags below (exact flag names TBD from Bisq 2 docs):
#   --networkServiceConfig.torSocksHost=${BISQ2_TOR_SOCKS_HOST:-tor}
#   --networkServiceConfig.torSocksPort=${BISQ2_TOR_SOCKS_PORT:-9050}

su -s /bin/sh appuser -c "exec '$JAVA_BIN' \
  -Xmx2g \
  -XX:MaxMetaspaceSize=256m \
  -XX:ReservedCodeCacheSize=128m \
  -XX:MaxDirectMemorySize=128m \
  -XX:+UseG1GC \
  -cp '/opt/bisq2/lib/*' \
  bisq.api_app.ApiApp \
  --data-dir='$APP_DIR' \
  --api.restEnabled=true \
  --api.bind.host=0.0.0.0 \
  --api.bind.port=8090 \
  --api.security.authorizationRequired=false" &

BISQ2_PID=$!

cleanup() {
  if kill -0 "$BISQ2_PID" 2>/dev/null; then
    kill "$BISQ2_PID" 2>/dev/null || true
    wait "$BISQ2_PID" 2>/dev/null || true
  fi
}
trap cleanup INT TERM

wait "$BISQ2_PID"
