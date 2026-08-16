# Database migrations

Keep forward-only database migration files in `db/migrations/`. New migrations
must be initialized by the migration runner; do not leave migration SQL in
`db/schema/`.
