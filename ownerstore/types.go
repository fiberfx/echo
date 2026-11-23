// SPDX-License-Identifier: MIT

package ownerstore

import (
	"errors"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

// MaxValue is the largest value, in bytes, that a store holds.
const MaxValue = 65536

// schemaVersion is the schema this package reads and writes.
const schemaVersion = 1

// Outcome is the outcome of a write.
type Outcome int

// The four outcomes of a write. The zero Outcome is not one of them.
const (
	Committed Outcome = iota + 1
	Conflict
	Refused
	Unknown
)

// String returns the name of the outcome as the store contract spells it.
func (o Outcome) String() string {
	switch o {
	case Committed:
		return "committed"
	case Conflict:
		return "conflict"
	case Refused:
		return "refused"
	case Unknown:
		return "unknown"
	}
	return "invalid"
}

// Class is a durability class of the store contract.
type Class string

// The classes this adapter serves. D3 (object storage) is not part of this package.
const (
	// D1: an acknowledgement means the change is in the operating system's file cache.
	D1 Class = "D1"
	// D2: an acknowledgement means the data is synced (and, on darwin, flushed with F_FULLFSYNC).
	D2 Class = "D2"
)

// Item is a stored row.
type Item struct {
	ID    reqid.EID
	Rev   uint64
	Value []byte
}

// Result is the answer of Insert and Update. Rev and Seq are those of the committed row and transaction; on
// Conflict, Rev and Stored are the row the call met (Rev 0 and a nil Stored when the key is absent).
type Result struct {
	Outcome Outcome
	Rev     uint64
	Seq     uint64
	Stored  []byte
}

// Page is one page of Scan. Next is the id to pass as after for the following page, set when More is.
type Page struct {
	Items []Item
	More  bool
	Next  reqid.EID
}

// Identity is what a store records about itself.
type Identity struct {
	StoreID reqid.EID
	Lineage [16]byte
	Class   Class
	Schema  int
}

// Report is the recovery report of the last open (or reopen) of a store.
type Report struct {
	// Seq is _store.seq as the open read it.
	Seq uint64
	// WALBytes is the length of the write-ahead log file before the open touched it.
	WALBytes int64
	// TornTailBytes is the length of the unacknowledged tail the log held after its last committed frame: frames of
	// the current log generation that were never committed or are torn. SQLite discards them.
	TornTailBytes int64
	// Reopens is the number of times the handle was reopened after a poisoning.
	Reopens int
}

// Cursor names a position in the changefeed of one store: the changes up to and including Seq are consumed.
type Cursor struct {
	StoreID reqid.EID
	Lineage [16]byte
	Seq     uint64
}

// Change is one changefeed row.
type Change struct {
	Seq   uint64
	Table string
	Key   []byte
	Op    int
}

// Operations of a Change.
const (
	OpInsert = 1
	OpUpdate = 2
)

// FeedPage is one page of Feed. Next is the cursor to pass for the following page.
type FeedPage struct {
	Changes []Change
	More    bool
	Next    Cursor
}

// The errors of Create and Open.
var (
	ErrExists   = errors.New("ownerstore: store exists")
	ErrNoStore  = errors.New("ownerstore: no store at the path")
	ErrHeld     = errors.New("ownerstore: store is held by another owner")
	ErrSettings = errors.New("ownerstore: settings differ from the declared class")
	ErrSchema   = errors.New("ownerstore: unsupported or missing schema")
	ErrClass    = errors.New("ownerstore: unknown durability class")
	ErrStoreID  = errors.New("ownerstore: store id is not an Ecd ID of kind sto")
	ErrClosed   = errors.New("ownerstore: store is closed")
)

// The outcome errors: Insert and Update return an error that matches the one of the outcome, and the cause.
var (
	ErrConflict = errors.New("ownerstore: conflict")
	ErrRefused  = errors.New("ownerstore: refused")
	ErrUnknown  = errors.New("ownerstore: unknown")
)

// The causes.
var (
	ErrTooLarge = errors.New("ownerstore: value over MaxValue bytes")
	ErrPoisoned = errors.New("ownerstore: handle is poisoned")
	ErrBusy     = errors.New("ownerstore: the write lock was not available in time")
	ErrBadKey   = errors.New("ownerstore: key is not an Ecd ID")
	ErrLimit    = errors.New("ownerstore: limit out of range")
)

// The errors of Feed.
var (
	// ErrGap: the cursor is below the oldest retained seq, or names another store or lineage. Nothing is skipped silently.
	ErrGap = errors.New("ownerstore: cursor gap")
	// ErrLineage is the cause of an ErrGap whose cursor names another store or another lineage.
	ErrLineage = errors.New("ownerstore: cursor names another store or lineage")
	// ErrCursorAhead: the cursor is beyond the store's position.
	ErrCursorAhead = errors.New("ownerstore: cursor is ahead of the store")
)
