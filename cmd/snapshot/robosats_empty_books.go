package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// defaultRobosatsEmptyBookCachePath sits next to the federation cache: the
	// container filesystem survives the restart between snapshot runs, and a
	// lost file only costs one round of re-probing.
	defaultRobosatsEmptyBookCachePath = "/tmp/robosats-empty-books.json"
	// robosatsEmptyBookSkipStep is how much longer each further consecutive
	// empty answer defers the next probe of that book.
	robosatsEmptyBookSkipStep = 10 * time.Minute
	// defaultRobosatsEmptyBookMaxSkip caps how long a new order on a previously
	// empty coordinator book can go unseen. RoboSats orders stay public for
	// hours, so a 30 min worst case trades little freshness for most of the
	// Tor round-trips (~2/3 of coordinator calls answer not_found).
	defaultRobosatsEmptyBookMaxSkip = 30 * time.Minute
	// robosatsEmptyBookForget drops entries for books not probed in a day, so
	// coordinators that left the federation do not accumulate.
	robosatsEmptyBookForget = 24 * time.Hour
)

// robosatsEmptyBook tracks consecutive not_found answers for one coordinator
// book (onion × currency × side).
type robosatsEmptyBook struct {
	Streak      int       `json:"streak"`
	LastProbeAt time.Time `json:"last_probe_at"`
}

// robosatsEmptyBookMemo remembers coordinator books that keep answering
// not_found so the snapshot worker stops paying a Tor round-trip for them on
// every cycle. The first empty answer never skips (a single empty response may
// be a blip); after that each further empty answer defers the next probe by
// another robosatsEmptyBookSkipStep, up to maxSkip. A real book or a failed
// request clears the entry, so a coordinator is never skipped while it holds
// orders or while it is erroring.
//
// Skipping is census-safe: a skipped book counts as a complete empty book, and
// a book is only skippable after it reported no orders, so no live offer can
// be closed because its coordinator was skipped. The cost is discovery delay
// for a new order on a book that was empty, bounded by maxSkip.
//
// The worker is one process per cycle, so the memo is persisted to path.
// RoboSats jobs for different currencies run concurrently; mu guards books.
type robosatsEmptyBookMemo struct {
	path    string
	maxSkip time.Duration

	mu     sync.Mutex
	loaded bool
	books  map[string]robosatsEmptyBook
}

func newRobosatsEmptyBookMemo(path string, maxSkip time.Duration) *robosatsEmptyBookMemo {
	return &robosatsEmptyBookMemo{path: path, maxSkip: maxSkip, books: map[string]robosatsEmptyBook{}}
}

// robosatsEmptyBookMaxSkip reads ROBOSATS_EMPTY_BOOK_MAX_SKIP; 0 disables
// skipping entirely.
func robosatsEmptyBookMaxSkip() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ROBOSATS_EMPTY_BOOK_MAX_SKIP"))
	if raw == "" {
		return defaultRobosatsEmptyBookMaxSkip
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		slog.Warn("invalid ROBOSATS_EMPTY_BOOK_MAX_SKIP; using default", "value", raw, "default", defaultRobosatsEmptyBookMaxSkip)
		return defaultRobosatsEmptyBookMaxSkip
	}
	return d
}

var robosatsEmptyBooks = newRobosatsEmptyBookMemo(defaultRobosatsEmptyBookCachePath, robosatsEmptyBookMaxSkip())

func robosatsEmptyBookKey(onion string, currencyID, offerType int) string {
	return onion + "|" + strconv.Itoa(currencyID) + "|" + strconv.Itoa(offerType)
}

// shouldSkip reports whether the book was empty recently enough that probing
// it again this cycle is not worth the Tor round-trip.
func (m *robosatsEmptyBookMemo) shouldSkip(key string, now time.Time) bool {
	if m == nil || m.maxSkip <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked(now)
	book, ok := m.books[key]
	if !ok || book.Streak < 2 {
		return false
	}
	skip := time.Duration(book.Streak-1) * robosatsEmptyBookSkipStep
	if skip > m.maxSkip {
		skip = m.maxSkip
	}
	return now.Before(book.LastProbeAt.Add(skip))
}

// recordEmpty notes a not_found answer for the book.
func (m *robosatsEmptyBookMemo) recordEmpty(key string, now time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked(now)
	book := m.books[key]
	book.Streak++
	book.LastProbeAt = now.UTC()
	m.books[key] = book
}

// forget clears the book after a real order book or a failed request.
func (m *robosatsEmptyBookMemo) forget(key string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked(time.Now())
	delete(m.books, key)
}

// save persists the memo. Failures only cost re-probing, so they are logged
// and otherwise ignored.
func (m *robosatsEmptyBookMemo) save() {
	if m == nil || m.path == "" {
		return
	}
	// Held across the write: concurrent RoboSats jobs each save, and an older
	// snapshot must never be renamed over a newer one.
	m.mu.Lock()
	defer m.mu.Unlock()
	body, err := json.Marshal(m.books)
	if err != nil {
		slog.Warn("could not encode RoboSats empty-book memo", "err", err)
		return
	}
	if err := writeFileAtomic(m.path, body); err != nil {
		slog.Warn("could not persist RoboSats empty-book memo", "path", m.path, "err", err)
	}
}

func (m *robosatsEmptyBookMemo) loadLocked(now time.Time) {
	if m.loaded {
		return
	}
	m.loaded = true
	if m.path == "" {
		return
	}
	f, err := os.Open(m.path)
	if err != nil {
		return
	}
	defer f.Close()
	body, err := readAllLimited(f)
	if err != nil {
		return
	}
	var books map[string]robosatsEmptyBook
	if err := json.Unmarshal(body, &books); err != nil {
		return
	}
	for key, book := range books {
		if book.Streak > 0 && now.Sub(book.LastProbeAt) < robosatsEmptyBookForget {
			m.books[key] = book
		}
	}
}

// writeFileAtomic writes body to path via a temp file + rename so a crash
// mid-write never leaves a truncated file behind.
func writeFileAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
