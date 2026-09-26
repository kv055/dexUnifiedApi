package dbutil

import (
	"io/fs"
	"time"
)

// ApplyDirWithLockRetry runs applyDir with a short lock policy so tests do not
// wait for the production timeouts.
func ApplyDirWithLockRetry(db *DB, fsys fs.FS, dir, table string, timeout time.Duration, attempts int, delay time.Duration) error {
	return applyDir(db, fsys, dir, table, map[string]bool{}, migrationLockRetry{
		timeout:  timeout,
		attempts: attempts,
		delay:    delay,
	})
}
