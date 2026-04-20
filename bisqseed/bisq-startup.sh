#!/bin/sh
set -eu

APP_DIR=/home/appuser/.local/share/Bisq
DB_DIR="$APP_DIR/btc_mainnet/db"
WALLET_FILE="$APP_DIR/btc_mainnet/wallet/bisq_BTC.wallet"
HELPER_CP="/opt/bisq/bisq-seed-helper:/opt/bisq/bisq-daemon-${BISQ_VERSION}/daemon.jar:/opt/bisq/bisq-daemon-${BISQ_VERSION}/lib/*"
JAVA_BIN=/opt/java/openjdk/bin/java
BACKUP_TIMEOUT_SEC="${BISQ_SEED_BACKUP_TIMEOUT_SEC:-900}"
BACKUP_RETRY_DELAY_SEC="${BISQ_SEED_BACKUP_RETRY_DELAY_SEC:-300}"
BACKUP_HELPER_TIMEOUT_SEC="${BISQ_SEED_BACKUP_HELPER_TIMEOUT_SEC:-30}"
ACCOUNT_PROVIDER="${BISQ_ACCOUNT_PROVIDER:-bisq}"

mkdir -p "$APP_DIR"
chown -R appuser:appuser "$APP_DIR"

if [ -d "$DB_DIR" ]; then
  echo '[startup] Purging re-syncable P2P data...'
  rm -f "$DB_DIR"/TradeStatistics3Store_* "$DB_DIR"/AccountAgeWitnessStore_* \
        "$DB_DIR/SignedWitnessStore" "$DB_DIR/AccountAgeWitnessStore" \
        "$DB_DIR/TradeStatistics3Store" "$DB_DIR/SequenceNumberMap" \
        "$DB_DIR/BlindVoteStore" \
        "$DB_DIR/ProposalStore" "$DB_DIR/TempProposalStore" \
        "$DB_DIR/BallotList" "$DB_DIR/BurningManAccountingStore_v3" \
        "$DB_DIR/RemovedPayloadsMap" "$DB_DIR/IgnoredMailboxMap" \
        "$DB_DIR/MailboxMessageList"
  rm -rf "$DB_DIR"/backup/backups_TradeStatistics3Store* \
         "$DB_DIR"/backup/backups_AccountAgeWitnessStore* \
         "$DB_DIR"/backup/backups_SignedWitnessStore* \
         "$DB_DIR"/backup/backups_SequenceNumberMap* \
         "$DB_DIR"/backup/backups_BlindVoteStore* \
         "$DB_DIR"/backup/backups_ProposalStore* \
         "$DB_DIR"/backup/backups_BallotList* \
         "$DB_DIR"/backup/backups_MailboxMessageList* \
         "$DB_DIR"/backup/backups_IgnoredMailboxMap*
  echo "[startup] Done. DB size: $(du -sh "$DB_DIR" | cut -f1)"
fi

su -s /bin/sh appuser -c "exec '$JAVA_BIN' \
  -Xmx2g \
  -XX:MaxMetaspaceSize=256m \
  -XX:ReservedCodeCacheSize=128m \
  -XX:MaxDirectMemorySize=128m \
  -XX:+UseG1GC \
  -jar /opt/bisq/bisq-daemon-${BISQ_VERSION}/daemon.jar \
  --apiPort=9998 \
  --apiPassword= \
  --appDataDir=/home/appuser/.local/share/Bisq \
  --nodePort=9999" &

BISQ_PID=$!

cleanup() {
  if kill -0 "$BISQ_PID" 2>/dev/null; then
    kill "$BISQ_PID" 2>/dev/null || true
    wait "$BISQ_PID" 2>/dev/null || true
  fi
}
trap cleanup INT TERM

if [ -n "${BISQ_SEED_BACKUP_DATABASE_URL:-}" ]; then
  deadline=$(( $(date +%s) + BACKUP_TIMEOUT_SEC ))
  while [ ! -f "$WALLET_FILE" ]; do
    if ! kill -0 "$BISQ_PID" 2>/dev/null; then
      echo "[startup] Bisq daemon exited before wallet seed backup ran" >&2
      wait "$BISQ_PID"
      exit 1
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "[startup] Timed out waiting for Bisq wallet file at $WALLET_FILE" >&2
      cleanup
      exit 1
    fi
    sleep 2
  done

  tmp_seed="$(mktemp)"
  seed_phrase=""
  wallet_created_at=""
  attempt=1
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if ! kill -0 "$BISQ_PID" 2>/dev/null; then
      echo "[startup] Bisq daemon exited before wallet seed extraction completed" >&2
      rm -f "$tmp_seed"
      wait "$BISQ_PID"
      exit 1
    fi

    if timeout "${BACKUP_HELPER_TIMEOUT_SEC}s" su -s /bin/sh appuser -c "'$JAVA_BIN' -cp '$HELPER_CP' ExtractBisqSeed '$WALLET_FILE' '${BISQ_WALLET_PASSWORD:-}'" >"$tmp_seed" 2>&1; then
      seed_phrase="$(awk '
        /^[a-z]+( [a-z]+){11,23}$/ {
          print
          exit
        }
      ' "$tmp_seed")"
      if [ -n "$seed_phrase" ]; then
        break
      fi
    fi

    if [ "$attempt" -eq 1 ]; then
      echo "[startup] Seed extraction did not succeed on the initial attempt; waiting ${BACKUP_RETRY_DELAY_SEC}s before retrying" >&2
    else
      echo "[startup] Seed extraction still not ready; waiting ${BACKUP_RETRY_DELAY_SEC}s before retry $((attempt + 1))" >&2
    fi
    attempt=$((attempt + 1))
    sleep "$BACKUP_RETRY_DELAY_SEC"
  done
  rm -f "$tmp_seed"

  if [ -z "$seed_phrase" ]; then
    echo "[startup] Failed to extract a Bisq wallet seed phrase before timeout; continuing without seed backup" >&2
    wait "$BISQ_PID"
    exit $?
  fi

  if ! psql \
    -v BISQ_ACCOUNT_PROVIDER_SQL="$ACCOUNT_PROVIDER" \
    -v BISQ_SEED_PHRASE_SQL="$seed_phrase" \
    -v BISQ_WALLET_CREATED_AT_SQL="$wallet_created_at" \
    "$BISQ_SEED_BACKUP_DATABASE_URL" <<'SQL'
\set ON_ERROR_STOP on
BEGIN;

INSERT INTO accounts (provider, email, password)
VALUES (:'BISQ_ACCOUNT_PROVIDER_SQL', NULL, NULL)
ON CONFLICT (provider) DO NOTHING;

WITH account_row AS (
    SELECT id
    FROM accounts
    WHERE provider = :'BISQ_ACCOUNT_PROVIDER_SQL'
),
matching_seed AS (
    SELECT 1
    FROM seeds s
    JOIN account_row a ON a.id = s.account_id
    WHERE s.seed_phrase = :'BISQ_SEED_PHRASE_SQL'
    LIMIT 1
)
INSERT INTO seeds (account_id, seed_phrase, wallet_created_at)
SELECT a.id,
       :'BISQ_SEED_PHRASE_SQL',
       NULLIF(:'BISQ_WALLET_CREATED_AT_SQL', '')::timestamptz
FROM account_row a
WHERE NOT EXISTS (
    SELECT 1
    FROM matching_seed ms
);

COMMIT;
SQL
  then
    echo "[startup] Failed to persist Bisq wallet seed backup; continuing without seed backup" >&2
    wait "$BISQ_PID"
    exit $?
  fi

  echo "[startup] Bisq wallet seed backup check completed for provider '$ACCOUNT_PROVIDER'"
else
  echo "[startup] BISQ_SEED_BACKUP_DATABASE_URL is not set; skipping seed backup"
fi

wait "$BISQ_PID"
