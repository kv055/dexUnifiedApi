# dexUnifiedApi

`dexUnifiedApi` owns the shared runtime substrate for dexPendulum: Postgres schema setup, live offer ingestion, Bisq snapshot support, payment notification collection, account-health checks, and logical database backups.

It is the service that other components depend on when they need a stable database schema or a single API for current DEX offer state.

## Runtime Role

On startup the server:

1. Loads `.env`.
2. Connects to Postgres as the admin role.
3. Ensures application roles exist for the unified API, monitor, MM controller, and fiat controller.
4. Opens the unified API database connection.
5. Checks that the required schema and migration files have already been applied; fails with a migration instruction if any are missing.
6. Starts CEX price feeds and trade-cost estimators.
7. Starts periodic Bisq account-health checks.
8. Serves public and internal HTTP endpoints on `LISTEN_ADDR`.

The `./server migrate` command bootstraps roles, then applies schema and payment-method seeds as `dex_unified_api`, preserving schema ownership and default grants. Deploy runs it before activating app containers, but only on images carrying the `dexarbtrading.unified-api.migrate` label from the `Dockerfile`: older binaries ignore the argument and start a full server. Startup still reconciles role passwords so rollback can restore the previous credentials, but neither the API nor snapshot worker applies schema migrations at startup. Migrations must remain backward-compatible with the previous release because rollback restores code and environment, not database contents.

The previous release keeps serving while migrations run. Each migration file therefore waits at most 5 seconds for a table lock (`lock_timeout`) and is retried up to 10 times after a lock timeout or deadlock, instead of queueing live queries behind a pending exclusive lock.

Every new file in `db/migrations/` must stay backward-compatible with the previous release, because rollback restores code but never the database. Follow expand/contract: one deploy only *adds* (nullable columns, tables, indexes, widened types), and a later deploy *removes* what the expand replaced. `DROP TABLE` and `DROP COLUMN` are never safe in the same deploy that stops using them. Each new file must end with a compat trailer stating its rollback story:

```sql
-- compat: backward-compatible
-- rollback: old code ignores the new nullable premium_bps column
```

`TestNewMigrationsDeclareRollbackCompatibility` enforces the trailer, fails on drops, and warns on renames, `ALTER COLUMN TYPE`, and `NOT NULL` adds without a surviving default. The check lints file shape only; it cannot prove old queries keep working, so think through the previous release's statements before shipping.

The snapshot worker is a separate binary/container. It talks to Bisq, HodlHodl, RoboSats, Tor, and price providers, writes snapshots to Postgres, and pushes the latest live offers to the API's internal endpoint.

## Directory Layout

```text
dexUnifiedApi/
+-- cmd/server/          # Public/internal HTTP API
+-- cmd/snapshot/        # Offer snapshot worker
+-- cmd/pgbackup/        # Postgres backup sidecar binary
+-- db/                  # Schema bootstrap and split schema files
+-- internal/            # Config, lifecycle, account health, payment notifications
+-- bisq_grpc/           # Bisq gRPC protobuf files
+-- bisqseed/            # Bisq account seed extraction/startup helpers
+-- Dockerfile*          # API, snapshot, Bisq, Tor, and backup images
+-- api-collection/      # REST client examples
```

## HTTP API

Default listen address: `:5010`.

Public endpoints:

- `GET /health`
- `GET /api/live_offers`
- `GET /api/live_offers/{id}/lifecycle`
- `GET /ws/live_offers`
- `GET /api/trade_cost_config`
- `GET /api/payment_notifications?limit=50`

Internal endpoints:

- `POST /internal/live_offers`
- `POST /internal/payment_notifications`

`/internal/live_offers` is used by the snapshot worker. `/internal/payment_notifications` is used by `dexFiatController` and accepts optional shared-secret auth through `X-Webhook-Secret`.

## Payment Notifications

Accepted fields:

- `amount`
- `currency`
- `type`
- `transaction_id`
- `dex`
- `connector`
- `transfer_message`
- `metadata`
- `timestamp`

Set `PAYMENT_NOTIFICATIONS_SHARED_SECRET` to require a matching `X-Webhook-Secret` header. Recent accepted notifications are available at:

```text
GET /api/payment_notifications?limit=50
```

## Environment

Copy the example file and fill in real values:

```bash
cp .env.example .env
```

Important variables:

- `DATABASE_URL`
- `POSTGRES_HOST`
- `POSTGRES_PORT`
- `POSTGRES_DB`
- `POSTGRES_ADMIN_USER`
- `POSTGRES_ADMIN_PASSWORD`
- `POSTGRES_UNIFIED_API_PASSWORD`
- `POSTGRES_MONITOR_PASSWORD`
- `POSTGRES_MM_CONTROLLER_PASSWORD`
- `POSTGRES_FIAT_CONTROLLER_PASSWORD`
- `LISTEN_ADDR`
- `SNAPSHOT_INTERVAL_MIN`
- `LIVE_OFFER_STALE_GRACE` (default `5m`; retain offers during source outages)
- `CLOSED_OFFER_ANALYSIS_GRACE_CYCLES`
- `TOR_PROXY_ADDR`
- `BISQ_GRPC_HOST`
- `BISQ_API_PASSWORD`
- `PRICE_PROVIDER`
- `TOR_PROXY_URL`
- `SERVER_INTERNAL_URL`
- `ACCOUNT_HEALTH_INTERVAL`
- `PAYMENT_NOTIFICATIONS_SHARED_SECRET`

In the root Compose stack most database values are injected from the root `.env`, while service-local defaults live in `dexUnifiedApi/.env`.

## Postgres Backups

The Compose stack includes a `postgres-backup` sidecar built from `Dockerfile.backup`. It runs `pg_dump -Fc`, checks the archive with `pg_restore --list`, and uploads it with `rclone copy`.

Local dumps are stored in the `postgres-backups` Docker volume. Files older than `PG_BACKUP_RETENTION_DAYS` are deleted locally.

Backup settings are read from the root `.env` used by Docker Compose:

```env
PG_BACKUP_ENABLED=true
PG_BACKUP_UPLOAD=true
PG_BACKUP_UPLOAD_REQUIRED=true
PG_BACKUP_RCLONE_REMOTE=proton:postgres-backups/dexarb
RCLONE_CONFIG_PROTON_TYPE=protondrive
RCLONE_CONFIG_PROTON_USERNAME=your@email.com
RCLONE_CONFIG_PROTON_PASSWORD=<rclone-obscured-password>
```

Rclone's Proton Drive backend is beta because Proton does not publish official API docs. Log into Proton Drive normally in a browser first so account keys are created, then test unattended uploads before relying on it.

Dropbox fallback:

```env
PG_BACKUP_RCLONE_REMOTE=dropbox:postgres-backups/dexarb
RCLONE_CONFIG_DROPBOX_TYPE=dropbox
RCLONE_CONFIG_DROPBOX_TOKEN=<oauth-token-json-from-rclone-config>
```

Useful checks:

```bash
docker compose build postgres-backup
docker compose run --rm postgres-backup rclone lsd proton:
docker compose run --rm postgres-backup rclone lsd dropbox:
```

For a local backup-only test:

```bash
PG_BACKUP_UPLOAD=false docker compose run --rm postgres-backup /app/pgbackup
```

## Local Development

With Postgres running and `.env` configured, migrate before running the API:

```bash
go run ./cmd/server migrate
go run ./cmd/server
```

Run the snapshot worker:

```bash
go run ./cmd/snapshot
```

Run tests:

```bash
go test ./...
```

For realistic local testing, use the root Compose stack so Postgres roles, Bisq, Tor, and service networking match production more closely.

## Operational Notes

- Schema ownership lives here. Other services should treat the shared tables as an integration contract.
- The live-offer hub intentionally keeps current offers in memory and receives fresh state from the snapshot worker.
- The API starts price feeds for supported CEXes so premium calculations can react to price changes.
- Account and secret storage schema files are part of this module because multiple services consume the same tables.
