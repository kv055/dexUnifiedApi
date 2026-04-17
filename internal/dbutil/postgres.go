package dbutil

import (
	"database/sql"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type DB struct {
	*sql.DB
}

type Tx struct {
	*sql.Tx
}

func Open(databaseURL string) (*DB, error) {
	raw, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	return &DB{DB: raw}, nil
}

func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.DB.Exec(rebind(query), args...)
}

func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.DB.Query(rebind(query), args...)
}

func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.DB.QueryRow(rebind(query), args...)
}

func (db *DB) Prepare(query string) (*sql.Stmt, error) {
	return db.DB.Prepare(rebind(query))
}

func (db *DB) Begin() (*Tx, error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &Tx{Tx: tx}, nil
}

func (tx *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.Tx.Exec(rebind(query), args...)
}

func (tx *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.Query(rebind(query), args...)
}

func (tx *Tx) QueryRow(query string, args ...any) *sql.Row {
	return tx.Tx.QueryRow(rebind(query), args...)
}

func (tx *Tx) Prepare(query string) (*sql.Stmt, error) {
	return tx.Tx.Prepare(rebind(query))
}

func rebind(query string) string {
	var out strings.Builder
	argIndex := 1
	inSingleQuote := false

	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch ch {
		case '\'':
			out.WriteByte(ch)
			if i+1 < len(query) && query[i+1] == '\'' {
				out.WriteByte(query[i+1])
				i++
				continue
			}
			inSingleQuote = !inSingleQuote
		case '?':
			if inSingleQuote {
				out.WriteByte(ch)
				continue
			}
			out.WriteByte('$')
			out.WriteString(strconvItoa(argIndex))
			argIndex++
		default:
			out.WriteByte(ch)
		}
	}

	return out.String()
}

func strconvItoa(v int) string {
	if v == 0 {
		return "0"
	}

	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + (v % 10))
		v /= 10
	}
	return string(buf[i:])
}
