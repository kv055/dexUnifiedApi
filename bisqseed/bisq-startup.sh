#!/bin/sh
set -eu

APP_DIR=/home/appuser/.local/share/Bisq
DB_DIR="$APP_DIR/btc_mainnet/db"
WALLET_FILE="$APP_DIR/btc_mainnet/wallet/bisq_BTC.wallet"
ACCOUNT_FILE="$DB_DIR/UserPayload"
MOUNTED_ACCOUNT_FILE="${BISQ_ACCOUNT_FILE_PATH:-}"
HELPER_CP="/opt/bisq/bisq-seed-helper:/opt/bisq/bisq-daemon-${BISQ_VERSION}/daemon.jar:/opt/bisq/bisq-daemon-${BISQ_VERSION}/lib/*"
JAVA_BIN=/opt/java/openjdk/bin/java
BACKUP_TIMEOUT_SEC="${BISQ_SEED_BACKUP_TIMEOUT_SEC:-900}"
BACKUP_RETRY_DELAY_SEC="${BISQ_SEED_BACKUP_RETRY_DELAY_SEC:-30}"
BACKUP_HELPER_TIMEOUT_SEC="${BISQ_SEED_BACKUP_HELPER_TIMEOUT_SEC:-30}"
ACCOUNT_PROVIDER="${BISQ_ACCOUNT_PROVIDER:-Bisq}"
BACKUP_DATABASE_URL="${BISQ_BACKUP_DATABASE_URL:-${BISQ_SEED_BACKUP_DATABASE_URL:-}}"

mkdir -p "$APP_DIR"
chown -R appuser:appuser "$APP_DIR" 2>/dev/null || \
  echo "[startup] Could not chown every Bisq data file; continuing in case some files are mounted read-only" >&2

install_account_file() {
  source_file="$1"
  source_description="$2"

  mkdir -p "$DB_DIR"
  tmp_account_file="$(mktemp)"
  cp "$source_file" "$tmp_account_file"
  chown appuser:appuser "$tmp_account_file"
  chmod 0600 "$tmp_account_file"
  mv "$tmp_account_file" "$ACCOUNT_FILE"
  chown -R appuser:appuser "$APP_DIR"
  echo "[startup] Restored Bisq account file from $source_description"
}

restore_account_file_from_database() {
  if [ -z "$BACKUP_DATABASE_URL" ]; then
    echo "[startup] No Bisq account file present and BISQ_BACKUP_DATABASE_URL is not set; Bisq will generate a new account"
    return 0
  fi

  tmp_restore_metadata="$(mktemp)"
  if ! psql -X -q -t -A -F '|' \
    -v BISQ_ACCOUNT_PROVIDER_SQL="$ACCOUNT_PROVIDER" \
    "$BACKUP_DATABASE_URL" >"$tmp_restore_metadata" <<'SQL'
\set ON_ERROR_STOP on
SELECT b.file_sha256 || '|' || b.file_size_bytes::text || '|' ||
       replace(replace(encode(b.contents, 'base64'), E'\n', ''), E'\r', '')
FROM bisq_account_file_backups b
JOIN accounts a ON a.id = b.account_id
WHERE a.provider = :'BISQ_ACCOUNT_PROVIDER_SQL'
ORDER BY b.created_at DESC, b.id DESC
LIMIT 1;
SQL
  then
    rm -f "$tmp_restore_metadata"
    echo "[startup] Failed to query latest Bisq account file backup; refusing to generate a fresh account while backup state is unknown" >&2
    exit 1
  fi

  if [ ! -s "$tmp_restore_metadata" ]; then
    rm -f "$tmp_restore_metadata"
    echo "[startup] No Bisq account file backup found in database; Bisq will generate a new account"
    return 0
  fi

  IFS='|' read -r expected_sha expected_size restored_base64 <"$tmp_restore_metadata"
  rm -f "$tmp_restore_metadata"

  tmp_account_file="$(mktemp)"
  if ! printf '%s' "$restored_base64" | base64 -d >"$tmp_account_file"; then
    rm -f "$tmp_account_file"
    echo "[startup] Failed to decode latest Bisq account file backup" >&2
    exit 1
  fi

  actual_sha="$(sha256sum "$tmp_account_file" | awk '{print $1}')"
  actual_size="$(stat -c '%s' "$tmp_account_file")"
  if [ "$actual_sha" != "$expected_sha" ] || [ "$actual_size" != "$expected_size" ]; then
    rm -f "$tmp_account_file"
    echo "[startup] Latest Bisq account file backup failed integrity check" >&2
    exit 1
  fi

  install_account_file "$tmp_account_file" "database backup $expected_sha"
  rm -f "$tmp_account_file"
}

restore_account_file_before_startup() {
  if [ -n "$MOUNTED_ACCOUNT_FILE" ]; then
    if [ ! -f "$MOUNTED_ACCOUNT_FILE" ]; then
      echo "[startup] BISQ_ACCOUNT_FILE_PATH is set but no file exists at $MOUNTED_ACCOUNT_FILE" >&2
      exit 1
    fi
    if [ -f "$ACCOUNT_FILE" ] && cmp -s "$MOUNTED_ACCOUNT_FILE" "$ACCOUNT_FILE"; then
      echo "[startup] Mounted Bisq account file already matches live account file"
      return 0
    fi
    install_account_file "$MOUNTED_ACCOUNT_FILE" "mounted file $MOUNTED_ACCOUNT_FILE"
    return 0
  fi

  if [ -f "$ACCOUNT_FILE" ]; then
    echo "[startup] Existing Bisq account file found at $ACCOUNT_FILE; keeping live account file"
    return 0
  fi

  restore_account_file_from_database
}

restore_account_file_before_startup

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

wait_for_file() {
  file_path="$1"
  description="$2"
  deadline="$3"
  while [ ! -f "$file_path" ]; do
    if ! kill -0 "$BISQ_PID" 2>/dev/null; then
      echo "[startup] Bisq daemon exited before $description backup ran" >&2
      wait "$BISQ_PID"
      exit 1
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "[startup] Timed out waiting for $description file at $file_path" >&2
      cleanup
      exit 1
    fi
    sleep 2
  done
}

if [ -n "$BACKUP_DATABASE_URL" ]; then
  deadline=$(( $(date +%s) + BACKUP_TIMEOUT_SEC ))
  wait_for_file "$ACCOUNT_FILE" "Bisq account" "$deadline"

  account_file_sha256="$(sha256sum "$ACCOUNT_FILE" | awk '{print $1}')"
  account_file_size="$(stat -c '%s' "$ACCOUNT_FILE")"
  account_file_modified_at="$(date -u -d "@$(stat -c '%Y' "$ACCOUNT_FILE")" '+%Y-%m-%dT%H:%M:%SZ')"
  account_file_base64="$(base64 -w 0 "$ACCOUNT_FILE")"

  if ! psql \
    -v BISQ_ACCOUNT_PROVIDER_SQL="$ACCOUNT_PROVIDER" \
    -v BISQ_ACCOUNT_FILE_NAME_SQL="$(basename "$ACCOUNT_FILE")" \
    -v BISQ_ACCOUNT_FILE_SOURCE_PATH_SQL="$ACCOUNT_FILE" \
    -v BISQ_ACCOUNT_FILE_SHA256_SQL="$account_file_sha256" \
    -v BISQ_ACCOUNT_FILE_SIZE_SQL="$account_file_size" \
    -v BISQ_ACCOUNT_FILE_MODIFIED_AT_SQL="$account_file_modified_at" \
    -v BISQ_ACCOUNT_FILE_BASE64_SQL="$account_file_base64" \
    "$BACKUP_DATABASE_URL" <<'SQL'
\set ON_ERROR_STOP on
BEGIN;

INSERT INTO accounts (provider, email, password)
VALUES (:'BISQ_ACCOUNT_PROVIDER_SQL', NULL, NULL)
ON CONFLICT (provider) DO NOTHING;

WITH account_row AS (
    SELECT id
    FROM accounts
    WHERE provider = :'BISQ_ACCOUNT_PROVIDER_SQL'
)
INSERT INTO bisq_account_file_backups (
    account_id,
    file_name,
    source_path,
    hash_algorithm,
    file_sha256,
    file_size_bytes,
    modified_at,
    contents
)
SELECT a.id,
       :'BISQ_ACCOUNT_FILE_NAME_SQL',
       NULLIF(:'BISQ_ACCOUNT_FILE_SOURCE_PATH_SQL', ''),
       'sha256',
       :'BISQ_ACCOUNT_FILE_SHA256_SQL',
       :'BISQ_ACCOUNT_FILE_SIZE_SQL'::bigint,
       NULLIF(:'BISQ_ACCOUNT_FILE_MODIFIED_AT_SQL', '')::timestamptz,
       decode(:'BISQ_ACCOUNT_FILE_BASE64_SQL', 'base64')
FROM account_row a
ON CONFLICT ON CONSTRAINT bisq_account_file_backups_account_hash_size_key DO NOTHING;

COMMIT;
SQL
  then
    echo "[startup] Failed to persist Bisq account file backup; continuing without account file backup" >&2
    wait "$BISQ_PID"
    exit $?
  fi

  echo "[startup] Bisq account file backup check completed for provider '$ACCOUNT_PROVIDER'"

  wait_for_file "$WALLET_FILE" "Bisq wallet" "$deadline"

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
        /BISQ_SEED_MNEMONIC=/ {
          sub(/^.*BISQ_SEED_MNEMONIC=/, "")
          print
          exit
        }
      ' "$tmp_seed")"
      if [ -n "$seed_phrase" ]; then
        wallet_created_at="$(awk '
          /BISQ_SEED_CREATED_AT=/ {
            sub(/^.*BISQ_SEED_CREATED_AT=/, "")
            print
            exit
          }
        ' "$tmp_seed")"
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
    "$BACKUP_DATABASE_URL" <<'SQL'
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
  echo "[startup] BISQ_BACKUP_DATABASE_URL is not set; skipping Bisq account file and seed backups"
fi

wait "$BISQ_PID"
