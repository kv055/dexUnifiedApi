# Database migrations

`db/schema/` is a frozen historical baseline. `db/migrations/` is the only
forward-only path for database changes.

- Name every new migration `YYYYMMDDHHMMSS_description.sql`, using a UTC
  creation timestamp and lowercase snake case for `description`. The timestamp
  must be unique and newer than every existing timestamped migration.
- Never rename, edit, delete, or reuse the filename of a migration that may
  have been deployed. Migration tracking is filename-based.
- Never clear `migration_migrations` or `schema_migrations` outside an
  intentionally discarded development database. Clearing either table causes
  already-applied SQL to run again.
- The numbered migration files are historical compatibility entries from the
  schema squash. Do not add files to `baselineMigrationFiles`; a newly added
  baseline entry is recorded without executing and can silently skip its SQL.
  Repair an old database state with a new, idempotent timestamped migration.
- Never add, edit, delete, rename, or reuse a `db/schema/` file. CI enforces
  this, along with immutability of the migration history. Add a new timestamped
  migration instead.
- Run `go test ./db` before deploying a migration change. The test suite
  rejects non-timestamped migration filenames except the explicit historical
  compatibility list.
