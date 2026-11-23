// SPDX-License-Identifier: MIT

package ownerstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/fiberfx/mx-echo/v6/reqid"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

const (
	busyTimeoutMS = 2000 // under the 3 s bound of a call
	readerConns   = 4
	// openMode is the SQLite open mode of every connection to an existing store. "rw" is the only thing that stops
	// SQLite from creating a missing file, because the driver always passes SQLITE_OPEN_CREATE.
	openMode = "rw"
)

// Store is an open owner store. All methods are safe for concurrent use.
type Store struct {
	path string
	opts options
	lock *fileLock

	// mu guards the pools: a call holds it for reading, reopen and Close hold it for writing.
	mu     sync.RWMutex
	w, r   *sql.DB
	closed bool
	id     Identity

	// pmu guards the poison state and the report.
	pmu     sync.Mutex
	poison  error
	report  Report
	reopens int

	// hooks are test seams; they are set before the calls they affect and read only by the calling goroutine.
	hooks hooks
}

// options are the seams of open.
type options struct {
	// syncOverride, when not empty, replaces the synchronous setting the writer's DSN asks for.
	syncOverride string
}

// hooks are test-only seams of the write path.
type hooks struct {
	// commit replaces the COMMIT of a write transaction.
	commit func(tx *sql.Tx) error
}

// pageURI returns the SQLite URI of the file at path: "file:" is what makes the driver pass the query on, and
// the mode in the query is the only guard that stops SQLite from creating a missing file.
func pageURI(path, mode string, pragmas ...string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Path: p}
	q := []string{"mode=" + mode}
	for _, x := range pragmas {
		q = append(q, "_pragma="+x)
	}
	return "file://" + u.EscapedPath() + "?" + strings.Join(q, "&")
}

// writerURI is the writer's DSN: the settings of a class, applied to every connection the pool opens.
func writerURI(path string, class Class, syncOverride string) string {
	mode := "NORMAL"
	if class == D2 {
		mode = "FULL"
	}
	if syncOverride != "" {
		mode = syncOverride
	}
	pr := []string{fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS), "synchronous(" + mode + ")"}
	if class == D2 {
		pr = append(pr, "fullfsync(1)", "checkpoint_fullfsync(1)")
	}
	return pageURI(path, openMode, pr...) + "&_txlock=immediate"
}

// readerURI is the DSN of the read pool.
func readerURI(path string) string {
	return pageURI(path, openMode, fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS), "query_only(1)")
}

func openDB(dsn string, conns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	return db, nil
}

// Open opens the store at path. It never creates, initialises, repairs or migrates: a missing store is ErrNoStore,
// a store held by another owner is ErrHeld, settings that differ from the store's class are ErrSettings, and a
// schema other than 1 is ErrSchema.
func Open(path string) (*Store, error) { return open(path, options{}) }

func open(path string, o options) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoStore, abs)
		}
		return nil, err
	}
	lk, err := lockFile(abs + ".lock")
	if err != nil {
		return nil, err
	}
	s := &Store{path: abs, opts: o, lock: lk}
	if err := s.openAll(context.Background()); err != nil {
		_ = lk.release()
		return nil, err
	}
	return s, nil
}

// openAll reads the store's identity, opens both pools, and reads the settings back. The caller holds the lock
// file, and mu for writing when the store is already in use.
func (s *Store) openAll(ctx context.Context) error {
	walBytes, torn := scanWAL(s.path + "-wal")
	id, seq, err := readIdentity(ctx, s.path)
	if err != nil {
		return err
	}
	w, err := openDB(writerURI(s.path, id.Class, s.opts.syncOverride), 1)
	if err != nil {
		return err
	}
	r, err := openDB(readerURI(s.path), readerConns)
	if err != nil {
		w.Close()
		return err
	}
	if err := checkSettings(ctx, w, id.Class); err != nil {
		w.Close()
		r.Close()
		return err
	}
	if err := r.PingContext(ctx); err != nil {
		w.Close()
		r.Close()
		return err
	}
	s.w, s.r, s.id = w, r, id
	s.pmu.Lock()
	s.report = Report{Seq: seq, WALBytes: walBytes, TornTailBytes: torn, Reopens: s.reopens}
	s.pmu.Unlock()
	return nil
}

// readIdentity reads _store through a short-lived connection, which is needed before the writer's DSN can be built:
// the class decides its synchronous setting.
func readIdentity(ctx context.Context, path string) (Identity, uint64, error) {
	db, err := openDB(pageURI(path, openMode, fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS), "query_only(1)"), 1)
	if err != nil {
		return Identity{}, 0, err
	}
	defer db.Close()
	var (
		sid, lin []byte
		schema   int
		class    string
		seq      int64
	)
	err = db.QueryRowContext(ctx, `SELECT store_id, lineage, schema, durability, seq FROM _store WHERE one = 1`).
		Scan(&sid, &lin, &schema, &class, &seq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no such table") {
			return Identity{}, 0, fmt.Errorf("%w: no _store row", ErrSchema)
		}
		return Identity{}, 0, err
	}
	if schema != schemaVersion {
		return Identity{}, 0, fmt.Errorf("%w: schema %d, this package serves %d", ErrSchema, schema, schemaVersion)
	}
	if class != string(D1) && class != string(D2) {
		return Identity{}, 0, fmt.Errorf("%w: durability %q", ErrSchema, class)
	}
	id := Identity{Class: Class(class), Schema: schema}
	if id.StoreID, err = reqid.EIDFromBytes(sid); err != nil || len(lin) != 16 {
		return Identity{}, 0, fmt.Errorf("%w: store_id or lineage", ErrSchema)
	}
	copy(id.Lineage[:], lin)
	return id, uint64(seq), nil
}

// checkSettings reads the settings back from the writer and compares them with the class.
func checkSettings(ctx context.Context, w *sql.DB, class Class) error {
	var mode string
	if err := w.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return err
	}
	if strings.ToLower(mode) != "wal" {
		return fmt.Errorf("%w: journal_mode is %q, not wal", ErrSettings, mode)
	}
	wantSync := 1
	if class == D2 {
		wantSync = 2
	}
	var gotSync int
	if err := w.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&gotSync); err != nil {
		return err
	}
	if gotSync != wantSync {
		return fmt.Errorf("%w: class %s wants synchronous=%d, the connection has %d", ErrSettings, class, wantSync, gotSync)
	}
	if class == D2 && runtime.GOOS == "darwin" {
		for _, p := range []string{"fullfsync", "checkpoint_fullfsync"} {
			var v int
			if err := w.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v); err != nil {
				return err
			}
			if v != 1 {
				return fmt.Errorf("%w: class D2 wants %s=1, the connection has %d", ErrSettings, p, v)
			}
		}
	}
	return nil
}

// Identity returns the store's identity.
func (s *Store) Identity() Identity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// Report returns the recovery report of the last open or reopen, with the reopen count as it stands.
func (s *Store) Report() Report {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	r := s.report
	r.Reopens = s.reopens
	return r
}

// Path returns the absolute path of the store file.
func (s *Store) Path() string { return s.path }

// Health is nil while the handle is open, unpoisoned and the writer answers SELECT 1.
func (s *Store) Health(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	if err := s.poisoned(); err != nil {
		return err
	}
	var one int
	return s.w.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Close closes the pools and releases the lock. It is idempotent.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.closePools()
	if lerr := s.lock.release(); err == nil {
		err = lerr
	}
	return err
}

func (s *Store) closePools() error {
	var err error
	if s.r != nil {
		err = s.r.Close()
	}
	if s.w != nil {
		if werr := s.w.Close(); err == nil {
			err = werr
		}
	}
	s.w, s.r = nil, nil
	return err
}

// poisonWith marks the handle poisoned. The first cause is kept.
func (s *Store) poisonWith(cause error) {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	if s.poison == nil {
		s.poison = cause
	}
}

// poisoned returns the refusal a poisoned handle answers writes with, or nil.
func (s *Store) poisoned() error {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	if s.poison == nil {
		return nil
	}
	return fmt.Errorf("%w: %w (first cause: %w)", ErrRefused, ErrPoisoned, s.poison)
}

// reopen closes both pools and opens them again, keeping the lock file, so that SQLite replays the log and the
// next read sees the durable state. A successful reopen clears the poison.
func (s *Store) reopen(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.pmu.Lock()
	bad := s.poison != nil
	s.pmu.Unlock()
	if !bad {
		return nil
	}
	_ = s.closePools()
	s.pmu.Lock()
	s.reopens++
	s.pmu.Unlock()
	if err := s.openAll(ctx); err != nil {
		return err
	}
	s.pmu.Lock()
	s.poison = nil
	s.pmu.Unlock()
	return nil
}

// reader returns the read pool, reopening the store first when the handle is poisoned. The caller calls release
// when its read is done.
func (s *Store) reader(ctx context.Context) (db *sql.DB, release func(), err error) {
	for range 3 {
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return nil, nil, ErrClosed
		}
		if s.poisoned() == nil && s.r != nil {
			return s.r, s.mu.RUnlock, nil
		}
		s.mu.RUnlock()
		if err := s.reopen(ctx); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, ErrPoisoned
}
