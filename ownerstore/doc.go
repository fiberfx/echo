// SPDX-License-Identifier: MIT

// Package ownerstore is the SQLite owner store of a service: one file, one writer process, keys that are Ecd IDs
// (reqid.EID, 16 bytes), values of at most MaxValue bytes, and the four outcomes of a write of the store contract
// of ecd (committed, conflict, refused, unknown).
//
// The store is a single SQLite 3 file in WAL mode, driven by modernc.org/sqlite (pure Go, no cgo). Create writes a
// new store and Open opens an existing one; Open never creates, initialises, repairs or migrates. Writes go through
// one connection, each in one BEGIN IMMEDIATE transaction that also moves the store's position (seq) by one and
// appends one row to the changefeed. Reads go through a pool of query-only connections, so a page of Scan is one
// snapshot.
//
// Outcomes. Insert and Update return a Result and an error that is nil only for Committed. For the other three
// outcomes errors.Is matches ErrConflict, ErrRefused or ErrUnknown, and also the cause when there is one
// (ErrTooLarge, ErrBusy, ErrPoisoned, a driver error). The store is in process, so it never answers "not sent".
//
//   - Conflict: a precondition failed (the key exists on Insert; the key is absent or its revision differs on Update);
//     nothing was written. The Result carries the row the call met in Rev and Stored.
//   - Refused: a rule or a resource bound rejected the request, or the store is poisoned; nothing was written.
//   - Unknown: the COMMIT may or may not have completed. The store poisons its handle: writes answer Refused with the
//     first cause, and the next read reopens the store, which lets SQLite run recovery, so that the read-back by the
//     request's own id sees the durable state. A retry of the same id after Unknown answers Conflict with the stored
//     value when the first attempt committed.
//
// A call's context bounds the wait for the writer and the statements before COMMIT. Once COMMIT has begun it runs to
// its real outcome, so a call may overrun its deadline by one commit.
//
// Durability. The class (D1 or D2) is recorded in the store at Create. Open reads the settings back (journal mode,
// synchronous, and fullfsync on darwin) and refuses to serve, with ErrSettings, when they differ from the class:
// D1 is synchronous=NORMAL, D2 is synchronous=FULL with fullfsync.
//
// Exclusion. Open takes an exclusive lock on "<path>.lock" until Close; a second Open of the same store, in this
// process or another, answers ErrHeld.
package ownerstore
