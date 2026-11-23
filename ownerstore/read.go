// SPDX-License-Identifier: MIT

package ownerstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

// MaxLimit is the largest page of Scan and Feed.
const MaxLimit = 1000

// Get reads the row of id. A read on a poisoned handle first reopens the store.
func (s *Store) Get(ctx context.Context, id reqid.EID) (Item, bool, error) {
	if _, err := reqid.EIDFromBytes(id[:]); err != nil {
		return Item{}, false, fmt.Errorf("%w: %w", ErrBadKey, err)
	}
	db, release, err := s.reader(ctx)
	if err != nil {
		return Item{}, false, err
	}
	defer release()
	var (
		rev int64
		val []byte
	)
	err = db.QueryRowContext(ctx, `SELECT rev, value FROM item WHERE id = ?1`, id[:]).Scan(&rev, &val)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	if val == nil {
		val = []byte{}
	}
	return Item{ID: id, Rev: uint64(rev), Value: val}, true, nil
}

// Scan reads up to limit rows with ids above after, in id order, in one snapshot. The zero EID starts at the
// first row. The limit is 1 to MaxLimit.
func (s *Store) Scan(ctx context.Context, after reqid.EID, limit int) (Page, error) {
	if limit < 1 || limit > MaxLimit {
		return Page{}, fmt.Errorf("%w: %d", ErrLimit, limit)
	}
	db, release, err := s.reader(ctx)
	if err != nil {
		return Page{}, err
	}
	defer release()
	rows, err := db.QueryContext(ctx, `SELECT id, rev, value FROM item WHERE id > ?1 ORDER BY id LIMIT ?2`, after[:], limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var p Page
	for rows.Next() {
		var (
			idb []byte
			rev int64
			val []byte
		)
		if err := rows.Scan(&idb, &rev, &val); err != nil {
			return Page{}, err
		}
		if len(p.Items) == limit {
			p.More = true
			break
		}
		var it Item
		copy(it.ID[:], idb)
		it.Rev = uint64(rev)
		it.Value = val
		if it.Value == nil {
			it.Value = []byte{}
		}
		p.Items = append(p.Items, it)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if p.More {
		p.Next = p.Items[len(p.Items)-1].ID
	}
	return p, nil
}

// Feed reads the changefeed after c: up to limit changes with seq above c.Seq, in seq order, in one snapshot.
//
// A cursor of another store or lineage, and a cursor below the oldest retained seq, answer ErrGap (the first with
// ErrLineage as its cause); a cursor beyond the store's position answers ErrCursorAhead. The feed is never read from
// a file offset and nothing is skipped silently. Every transaction writes one row, so a page ends on a seq boundary.
func (s *Store) Feed(ctx context.Context, c Cursor, limit int) (FeedPage, error) {
	if limit < 1 || limit > MaxLimit {
		return FeedPage{}, fmt.Errorf("%w: %d", ErrLimit, limit)
	}
	id := s.Identity()
	if c.StoreID != id.StoreID || c.Lineage != id.Lineage {
		return FeedPage{}, fmt.Errorf("%w: %w", ErrGap, ErrLineage)
	}
	db, release, err := s.reader(ctx)
	if err != nil {
		return FeedPage{}, err
	}
	defer release()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return FeedPage{}, err
	}
	defer tx.Rollback()
	var head int64
	if err := tx.QueryRowContext(ctx, seqSQL).Scan(&head); err != nil {
		return FeedPage{}, err
	}
	if c.Seq > uint64(head) {
		return FeedPage{}, fmt.Errorf("%w: cursor %d, store at %d", ErrCursorAhead, c.Seq, head)
	}
	oldest := head + 1
	var lo sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT min(seq) FROM _feed`).Scan(&lo); err != nil {
		return FeedPage{}, err
	}
	if lo.Valid {
		oldest = lo.Int64
	}
	if c.Seq+1 < uint64(oldest) {
		return FeedPage{}, fmt.Errorf("%w: cursor %d, oldest retained seq %d", ErrGap, c.Seq, oldest)
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq, tbl, key, op FROM _feed WHERE seq > ?1 ORDER BY seq, tbl, key LIMIT ?2`, int64(c.Seq), limit+1)
	if err != nil {
		return FeedPage{}, err
	}
	defer rows.Close()
	p := FeedPage{Next: c}
	for rows.Next() {
		var (
			ch  Change
			seq int64
		)
		if err := rows.Scan(&seq, &ch.Table, &ch.Key, &ch.Op); err != nil {
			return FeedPage{}, err
		}
		if len(p.Changes) == limit {
			p.More = true
			break
		}
		ch.Seq = uint64(seq)
		p.Changes = append(p.Changes, ch)
		p.Next.Seq = ch.Seq
	}
	if err := rows.Err(); err != nil {
		return FeedPage{}, err
	}
	return p, nil
}
