// SPDX-License-Identifier: MIT

package ownerstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

// The statements of a write. Each write is one BEGIN IMMEDIATE transaction: the conditional statement, the bump of
// _store.seq, and one _feed row that carries the new seq.
const (
	insertSQL = `INSERT INTO item (id, rev, value, seq) VALUES (?1, 1, ?2, (SELECT seq + 1 FROM _store WHERE one = 1)) ` +
		`ON CONFLICT (id) DO NOTHING`
	updateSQL = `UPDATE item SET rev = rev + 1, value = ?1, seq = (SELECT seq + 1 FROM _store WHERE one = 1) ` +
		`WHERE id = ?2 AND rev = ?3`
	bumpSQL = `UPDATE _store SET seq = seq + 1 WHERE one = 1`
	feedSQL = `INSERT INTO _feed (seq, tbl, key, op) SELECT seq, 'item', ?1, ?2 FROM _store WHERE one = 1`
	seqSQL  = `SELECT seq FROM _store WHERE one = 1`
	metSQL  = `SELECT rev, value FROM item WHERE id = ?1`
)

// Insert stores value under id when id is absent. A second Insert of the same id answers Conflict, with the stored
// row in the Result.
func (s *Store) Insert(ctx context.Context, id reqid.EID, value []byte) (Result, error) {
	return s.write(ctx, id, true, 0, value)
}

// Update replaces the value of id when its revision is rev, and increments the revision. An absent id and a
// different revision both answer Conflict.
func (s *Store) Update(ctx context.Context, id reqid.EID, rev uint64, value []byte) (Result, error) {
	return s.write(ctx, id, false, rev, value)
}

func refused(cause error) (Result, error) {
	return Result{Outcome: Refused}, outcomeError(Refused, cause)
}

func (s *Store) write(ctx context.Context, id reqid.EID, insert bool, rev uint64, value []byte) (Result, error) {
	if _, err := reqid.EIDFromBytes(id[:]); err != nil {
		return refused(fmt.Errorf("%w: %w", ErrBadKey, err))
	}
	if len(value) > MaxValue {
		return refused(ErrTooLarge)
	}
	if value == nil {
		value = []byte{}
	}
	if err := ctx.Err(); err != nil {
		return refused(err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return Result{}, ErrClosed
	}
	if err := s.poisoned(); err != nil {
		return Result{Outcome: Refused}, err
	}
	w, err := s.w.Conn(ctx)
	if err != nil {
		return refused(err)
	}
	defer w.Close()
	if err := s.poisoned(); err != nil {
		return Result{Outcome: Refused}, err
	}
	return s.run(ctx, w, id, insert, rev, value)
}

// run executes one write on the writer connection w.
func (s *Store) run(ctx context.Context, w *sql.Conn, id reqid.EID, insert bool, rev uint64, value []byte) (Result, error) {
	bg := context.Background()
	tx, err := w.BeginTx(bg, nil)
	if err != nil {
		return s.failBefore(nil, err)
	}
	res, err := s.statements(bg, tx, id, insert, rev, value)
	if err != nil {
		return s.failBefore(tx, err)
	}
	if res.Outcome == Conflict {
		if err := tx.Rollback(); err != nil && !noTransaction(err) {
			s.poisonWith(err)
		}
		return res, outcomeError(Conflict, nil)
	}
	if err := ctx.Err(); err != nil {
		return s.failBefore(tx, err)
	}
	commit := s.hooks.commit
	if commit == nil {
		commit = (*sql.Tx).Commit
	}
	if err := commit(tx); err != nil {
		return s.failCommit(w, tx, err)
	}
	return res, nil
}

// statements runs the conditional statement, and for a change the seq bump and the _feed row.
func (s *Store) statements(ctx context.Context, tx *sql.Tx, id reqid.EID, insert bool, rev uint64, value []byte) (Result, error) {
	var (
		r   sql.Result
		err error
		op  = OpUpdate
	)
	if insert {
		op = OpInsert
		r, err = tx.ExecContext(ctx, insertSQL, id[:], value)
	} else {
		r, err = tx.ExecContext(ctx, updateSQL, value, id[:], int64(rev))
	}
	if err != nil {
		return Result{}, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return Result{}, err
	}
	if n == 0 {
		return met(ctx, tx, id)
	}
	if _, err := tx.ExecContext(ctx, bumpSQL); err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, feedSQL, id[:], op); err != nil {
		return Result{}, err
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, seqSQL).Scan(&seq); err != nil {
		return Result{}, err
	}
	res := Result{Outcome: Committed, Rev: 1, Seq: uint64(seq)}
	if !insert {
		res.Rev = rev + 1
	}
	return res, nil
}

// met reads the row a conditional statement met, for the Result of a Conflict.
func met(ctx context.Context, tx *sql.Tx, id reqid.EID) (Result, error) {
	var (
		rev    int64
		stored []byte
	)
	err := tx.QueryRowContext(ctx, metSQL, id[:]).Scan(&rev, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{Outcome: Conflict}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if stored == nil {
		stored = []byte{}
	}
	return Result{Outcome: Conflict, Rev: uint64(rev), Stored: stored}, nil
}

// failBefore ends a write whose error came before COMMIT: the transaction (when one began) is rolled back, and
// nothing was committed, so the outcome is Refused.
func (s *Store) failBefore(tx *sql.Tx, err error) (Result, error) {
	rolled := func() bool {
		if tx == nil {
			return true
		}
		rerr := tx.Rollback()
		return rerr == nil || noTransaction(rerr)
	}
	v := classify(beforeCommit, err, rolled)
	if v.poison {
		s.poisonWith(v.cause)
	}
	return Result{Outcome: v.outcome}, outcomeError(v.outcome, v.cause)
}

// failCommit ends a write whose COMMIT returned err. Whether the transaction is gone decides BUSY, LOCKED and
// FULL; every other error leaves the outcome unknown.
func (s *Store) failCommit(w *sql.Conn, tx *sql.Tx, err error) (Result, error) {
	bg := context.Background()
	probe := func() bool {
		if sqliteCode(err) == sqliteFull {
			return autocommit(bg, w)
		}
		_, rerr := w.ExecContext(bg, "ROLLBACK")
		return rerr == nil || noTransaction(rerr)
	}
	v := classify(atCommit, err, probe)
	if v.poison {
		s.poisonWith(v.cause)
	}
	// Best effort: leave no transaction open on the connection the pool takes back. The Tx is done after a
	// COMMIT that ran, and then Rollback says so.
	_ = tx.Rollback()
	_, _ = w.ExecContext(bg, "ROLLBACK")
	return Result{Outcome: v.outcome}, outcomeError(v.outcome, v.cause)
}

// autocommit reports whether the connection is back in autocommit mode, so that SQLite rolled the transaction
// back. It begins a transaction: that succeeds only in autocommit mode, and is then rolled back; inside a
// transaction BEGIN fails.
func autocommit(ctx context.Context, w *sql.Conn) bool {
	if _, err := w.ExecContext(ctx, "BEGIN"); err != nil {
		return false
	}
	_, _ = w.ExecContext(ctx, "ROLLBACK")
	return true
}
