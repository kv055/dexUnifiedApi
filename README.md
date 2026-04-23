# dexUnifiedApi

## Postgres Backups

The Compose stack includes a `postgres-backup` sidecar that runs `pg_dump -Fc`,
checks the archive with `pg_restore --list`, and uploads it with `rclone copy`.
It stores local dumps in the `postgres-backups` Docker volume and deletes local
files older than `PG_BACKUP_RETENTION_DAYS`.

The sidecar reads backup and rclone settings from the root `.env` used by Docker
Compose. Set `PG_BACKUP_ENABLED=false` to leave the service running but skip
dump creation and uploads entirely. By default it tries Proton Drive:

```env
PG_BACKUP_ENABLED=true
PG_BACKUP_UPLOAD=true
PG_BACKUP_UPLOAD_REQUIRED=true
PG_BACKUP_RCLONE_REMOTE=proton:postgres-backups/dexarb
RCLONE_CONFIG_PROTON_TYPE=protondrive
RCLONE_CONFIG_PROTON_USERNAME=your@email.com
RCLONE_CONFIG_PROTON_PASSWORD=<rclone-obscured-password>
```

Rclone's Proton Drive backend is beta because Proton does not publish official
API docs. Log into Proton Drive normally in a browser first so account keys are
created, then test unattended uploads before relying on it.

Dropbox is the fallback:

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

For a local backup-only test, set `PG_BACKUP_UPLOAD=false` in the root `.env`,
then run:

```bash
docker compose run --rm postgres-backup /app/pgbackup
```

## Payment Notifications

`dexUnifiedApi` now exposes a webhook receiver for fiat payment notifications.

- `POST /internal/payment_notifications`
- Optional auth via `PAYMENT_NOTIFICATIONS_SHARED_SECRET` and the `X-Webhook-Secret` header
- Accepted payload fields: `amount`, `currency`, `type`, `transaction_id`, `dex`, `connector`, `transfer_message`, `metadata`, `timestamp`
- Recent accepted notifications are available at `GET /api/payment_notifications?limit=50`

This gives the connectors and the fiat controller a stable contract now, while subscriber fan-out can be layered onto the in-memory hub later.
