// SPDX-License-Identifier: MIT

package ownerstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var bg = context.Background()

// ---- create and open -----------------------------------------------------------------------------------------

func TestCreateSettings(t *testing.T) {
	s, path := newStore(t, D2)
	id := s.Identity()
	if id.Class != D2 || id.Schema != 1 || id.StoreID != mkID(t, "sto", 1) || id.Lineage == ([16]byte{}) {
		t.Fatalf("identity %+v", id)
	}
	if got := rawQuery(t, s, "PRAGMA auto_vacuum"); len(got) != 1 || got[0] != "2" {
		t.Fatalf("auto_vacuum %v, want 2 (incremental)", got)
	}
	if got := rawQuery(t, s, "PRAGMA journal_mode"); len(got) != 1 || got[0] != "wal" {
		t.Fatalf("journal_mode %v", got)
	}
	var names []string
	rows, err := rawReader(t, path).Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	if strings.Join(names, ",") != "_feed,_store,item" {
		t.Fatalf("tables %v", names)
	}
	if err := Create(path+"x", mkID(t, "itm", 1), D1); !errors.Is(err, ErrStoreID) {
		t.Fatalf("a store id of another kind: %v", err)
	}
	if err := Create(path+"y", mkID(t, "sto", 1), "D0"); !errors.Is(err, ErrClass) {
		t.Fatalf("a class D0: %v", err)
	}
}

func TestCreateExisting(t *testing.T) {
	s, path := newStore(t, D1)
	want := s.Identity()
	s.Close()
	if err := Create(path, mkID(t, "sto", 2), D2); !errors.Is(err, ErrExists) {
		t.Fatalf("Create over an existing store: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.Identity(); got != want {
		t.Fatalf("identity changed: %+v, was %+v", got, want)
	}
}

// TestCreateFailedLeavesNone is row create_failed_leaves_none: a fault before the rename leaves no store under the
// final name and no stray file.
func TestCreateFailedLeavesNone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	testCreateHook = func(stage string) error { return fmt.Errorf("injected fault at %s", stage) }
	t.Cleanup(func() { testCreateHook = nil })
	if err := Create(path, mkID(t, "sto", 1), D2); err == nil || !strings.Contains(err.Error(), "injected fault at synced") {
		t.Fatalf("Create: %v", err)
	}
	testCreateHook = nil
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("files left after a failed Create: %v", ents)
	}
	if _, err := Open(path); !errors.Is(err, ErrNoStore) {
		t.Fatalf("Open after a failed Create: %v", err)
	}
	if err := Create(path, mkID(t, "sto", 1), D2); err != nil {
		t.Fatalf("Create after the fault is gone: %v", err)
	}
}

// TestOpenMissing is row open_missing_refuses: Open of a missing store refuses and no file appears, the lock file
// included; the identity read, which Open would run if the store vanished after its check, creates nothing either.
func TestOpenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if _, err := Open(path); !errors.Is(err, ErrNoStore) {
		t.Fatalf("Open: %v", err)
	}
	if _, _, err := readIdentity(bg, path); err == nil {
		t.Fatal("readIdentity of a missing file succeeded")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("files appeared: %v", ents)
	}
}

// TestOpenDoesNotMigrate is row open_does_not_migrate: a store of another schema version is refused, and nothing
// is added to it.
func TestOpenDoesNotMigrate(t *testing.T) {
	s, path := newStore(t, D1)
	rawExec(t, s, `UPDATE _store SET schema = 0`)
	s.Close()
	list := func() string {
		rows, err := rawReader(t, path).Query(`SELECT type, name, sql FROM sqlite_master ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var a, n, q string
			rows.Scan(&a, &n, &q)
			fmt.Fprintf(&b, "%s|%s|%s\n", a, n, q)
		}
		return b.String()
	}
	before := list()
	if _, err := Open(path); !errors.Is(err, ErrSchema) {
		t.Fatalf("Open of schema 0: %v", err)
	}
	if after := list(); after != before {
		t.Fatalf("Open changed the schema:\n%s\n---\n%s", before, after)
	}
}

// TestSecondOwner is row second_writer_refused: a second Open of a held store is refused, in this process and in
// another, and the first keeps serving; when the holder dies the store opens again.
func TestSecondOwner(t *testing.T) {
	s, path := newStore(t, D1)
	if _, err := Open(path); !errors.Is(err, ErrHeld) {
		t.Fatalf("second Open in process: %v", err)
	}
	if res, err := s.Insert(bg, mkID(t, "itm", 1), []byte("a")); err != nil || res.Outcome != Committed {
		t.Fatalf("the first owner stopped serving: %v %v", res, err)
	}
	s.Close()
	c := startChild(t, "hold", path, 0, 0)
	c.waitLine(t, "HELD")
	if _, err := Open(path); !errors.Is(err, ErrHeld) {
		t.Fatalf("Open while another process holds the store: %v", err)
	}
	c.kill()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open after the holder was killed: %v", err)
	}
	s2.Close()
}

// ---- classes -------------------------------------------------------------------------------------------------

// TestClassReadBack is rows settings_equal_class_d1 and settings_equal_class_d2: the settings read back at open equal
// the declared class and the store serves.
func TestClassReadBack(t *testing.T) {
	for _, tc := range []struct {
		class Class
		want  string
	}{{D1, "1"}, {D2, "2"}} {
		t.Run(string(tc.class), func(t *testing.T) {
			s, _ := newStore(t, tc.class)
			if got := rawQuery(t, s, "PRAGMA synchronous"); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("synchronous %v, want %s", got, tc.want)
			}
			if tc.class == D2 {
				if got := rawQuery(t, s, "PRAGMA fullfsync"); len(got) != 1 || got[0] != "1" {
					t.Fatalf("fullfsync %v", got)
				}
			}
			if res, err := s.Insert(bg, mkID(t, "itm", 1), []byte("x")); err != nil || res.Outcome != Committed {
				t.Fatalf("the store does not serve: %v %v", res, err)
			}
		})
	}
}

// TestClassPlant is row settings_mismatch_refuses: a D2 store opened with synchronous=NORMAL is refused, and the
// lock it took is released.
func TestClassPlant(t *testing.T) {
	s, path := newStore(t, D2)
	s.Close()
	if st, err := open(path, options{syncOverride: "NORMAL"}); !errors.Is(err, ErrSettings) {
		if st != nil {
			st.Close()
		}
		t.Fatalf("open with synchronous=NORMAL: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open after the refused open: %v", err)
	}
	s2.Close()
}

// TestDeclaredClassReported is row declared_class_reported: the identity reports the class the store was created with,
// and health is nil.
func TestDeclaredClassReported(t *testing.T) {
	for _, class := range []Class{D1, D2} {
		s, path := newStore(t, class)
		if s.Identity().Class != class || s.Health(bg) != nil {
			t.Fatalf("%s: identity %+v health %v", class, s.Identity(), s.Health(bg))
		}
		s.Close()
		s2, err := Open(path)
		if err != nil || s2.Identity().Class != class {
			t.Fatalf("%s: reopened %v %v", class, s2, err)
		}
		s2.Close()
	}
}

// TestD2NoAckAfterFailedSync is row d2_ack_after_sync: when the sync fails, no acknowledgement is sent, and the
// outcome is not committed.
func TestD2NoAckAfterFailedSync(t *testing.T) {
	s, _ := newStore(t, D2)
	s.hooks.commit = rollbackThenFail(&fakeErr{codeIOFsync})
	id := mkID(t, "itm", 1)
	res, err := s.Insert(bg, id, []byte("v"))
	if res.Outcome == Committed || err == nil || !errors.Is(err, ErrUnknown) {
		t.Fatalf("a failed sync: %v %v", res, err)
	}
	s.hooks.commit = nil
	if _, found, err := s.Get(bg, id); err != nil || found {
		t.Fatalf("read-back: found %v err %v", found, err)
	}
}

// TestKillKeepsWrite is row d1_survives_kill9 (and its D2 twin): a write the child saw returned survives SIGKILL, for
// three cycles of open, write, kill.
func TestKillKeepsWrite(t *testing.T) {
	for _, class := range []Class{D1, D2} {
		t.Run(string(class), func(t *testing.T) {
			s, path := newStore(t, class)
			s.Close()
			for cycle := 0; cycle < 3; cycle++ {
				c := startChild(t, "write", path, cycle*100, 5)
				c.waitLine(t, fmt.Sprintf("ACK %d", cycle*100+4))
				c.kill()
				s, err := Open(path)
				if err != nil {
					t.Fatalf("cycle %d: Open: %v", cycle, err)
				}
				for i := 0; i < 5; i++ {
					it, found, err := s.Get(bg, mkID(t, "itm", cycle*100+i))
					if err != nil || !found || string(it.Value) != fmt.Sprintf("value-%d", cycle*100+i) {
						t.Fatalf("cycle %d item %d: found %v err %v value %q", cycle, i, found, err, it.Value)
					}
				}
				if got := s.Report().Seq; got != uint64(5*(cycle+1)) {
					t.Fatalf("cycle %d: seq %d", cycle, got)
				}
				s.Close()
			}
		})
	}
}

// ---- recovery ------------------------------------------------------------------------------------------------

// TestRecoveryReport is row recovery_reports_position: after a kill the report gives _store.seq, the length of the log at
// open and the reopen count, which a poisoning and its reopen move.
func TestRecoveryReport(t *testing.T) {
	s, path := newStore(t, D1)
	s.Close()
	c := startChild(t, "write", path, 0, 7)
	c.waitLine(t, "ACK 6")
	c.kill()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.Report()
	if r.Seq != 7 || r.WALBytes <= 0 || r.Reopens != 0 || r.TornTailBytes != 0 {
		t.Fatalf("report after a kill: %+v", r)
	}
	s.hooks.commit = commitThenFail(&fakeErr{codeIOFsync})
	if res, _ := s.Insert(bg, mkID(t, "itm", 50), []byte("z")); res.Outcome != Unknown {
		t.Fatalf("injected fault: %v", res)
	}
	s.hooks.commit = nil
	if _, found, err := s.Get(bg, mkID(t, "itm", 50)); err != nil || !found {
		t.Fatalf("read-back: found %v err %v", found, err)
	}
	if r := s.Report(); r.Reopens != 1 || r.Seq != 8 {
		t.Fatalf("report after a reopen: %+v", r)
	}
}

// TestRecoveryTornTail is row recovery_torn_tail_discarded: a torn frame after the acknowledged ones is discarded, the report
// counts it, and no acknowledged write is lost.
func TestRecoveryTornTail(t *testing.T) {
	s, path := newStore(t, D1)
	s.Close()
	c := startChild(t, "write", path, 0, 5)
	c.waitLine(t, "ACK 4")
	c.kill()
	wal, err := os.ReadFile(path + "-wal")
	if err != nil || len(wal) < 32+24+512 {
		t.Fatalf("log: %d bytes, %v", len(wal), err)
	}
	// A torn frame: the first frame's header (same salts) and the first 100 bytes of its page.
	torn := append([]byte(nil), wal[32:32+24+100]...)
	f, err := os.OpenFile(path+"-wal", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(torn); err != nil {
		t.Fatal(err)
	}
	f.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if r := s.Report(); r.TornTailBytes != int64(len(torn)) || r.Seq != 5 {
		t.Fatalf("report: %+v, torn tail %d bytes expected", r, len(torn))
	}
	for i := 0; i < 5; i++ {
		if _, found, err := s.Get(bg, mkID(t, "itm", i)); err != nil || !found {
			t.Fatalf("acknowledged item %d: found %v err %v", i, found, err)
		}
	}
	if res, err := s.Insert(bg, mkID(t, "itm", 9), []byte("after")); err != nil || res.Outcome != Committed || res.Seq != 6 {
		t.Fatalf("a write after recovery: %v %v", res, err)
	}
}

// ---- feed ----------------------------------------------------------------------------------------------------

// TestSeqContiguous is row seq_no_gap_10000: seq runs from 1 by one with no gap across the transactions, and equals
// _store.seq at the end.
func TestSeqContiguous(t *testing.T) {
	s, _ := newStore(t, D1)
	for i := 1; i <= feedTxns; i++ {
		res, err := s.Insert(bg, mkID(t, "itm", i), []byte("v"))
		if err != nil || res.Outcome != Committed || res.Seq != uint64(i) {
			t.Fatalf("write %d: %v %v", i, res, err)
		}
	}
	if res, _ := s.Insert(bg, mkID(t, "itm", 1), []byte("v")); res.Outcome != Conflict {
		t.Fatalf("a conflict: %v", res)
	}
	got := rawQuery(t, s, `SELECT count(*), count(DISTINCT seq), min(seq), max(seq), (SELECT seq FROM _store) FROM _feed`)
	want := fmt.Sprint([]string{fmt.Sprint(feedTxns), fmt.Sprint(feedTxns), "1", fmt.Sprint(feedTxns), fmt.Sprint(feedTxns)})
	if fmt.Sprint(got) != want {
		t.Fatalf("feed %v, want %v", got, want)
	}
}

// TestFeedRowInTransaction is row feed_row_in_transaction: a write that rolls back leaves no feed row and does not move seq.
func TestFeedRowInTransaction(t *testing.T) {
	s, _ := newStore(t, D1)
	a, b := mkID(t, "itm", 1), mkID(t, "itm", 2)
	if res, err := s.Insert(bg, a, []byte("1")); err != nil || res.Outcome != Committed {
		t.Fatal(res, err)
	}
	check := func(what string) {
		t.Helper()
		got := rawQuery(t, s, `SELECT (SELECT count(*) FROM _feed), (SELECT seq FROM _store)`)
		if fmt.Sprint(got) != "[1 1]" {
			t.Fatalf("%s: feed rows and seq %v, want [1 1]", what, got)
		}
	}
	s.Insert(bg, a, []byte("again"))
	check("a conflict")
	s.Update(bg, a, 7, []byte("stale"))
	check("a stale update")
	s.Insert(bg, b, make([]byte, MaxValue+1))
	check("an oversized value")
	s.hooks.commit = rollbackThenFail(&fakeErr{codeIOErr})
	if res, _ := s.Insert(bg, b, []byte("2")); res.Outcome != Unknown {
		t.Fatalf("rolled back after COMMIT: %v", res)
	}
	s.hooks.commit = nil
	s.Get(bg, b) // reopens the poisoned handle
	check("a rolled-back commit")
}

func feedFixture(t *testing.T, n int) (*Store, Cursor) {
	s, _ := newStore(t, D1)
	for i := 1; i <= n; i++ {
		if _, err := s.Insert(bg, mkID(t, "itm", i), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	id := s.Identity()
	return s, Cursor{StoreID: id.StoreID, Lineage: id.Lineage}
}

// TestFeedPaging reads the feed in pages and finds every seq once.
func TestFeedPaging(t *testing.T) {
	s, cur := feedFixture(t, 10)
	var seqs []uint64
	for {
		p, err := s.Feed(bg, cur, 4)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range p.Changes {
			seqs = append(seqs, c.Seq)
			if c.Table != "item" || c.Op != OpInsert || len(c.Key) != 16 {
				t.Fatalf("change %+v", c)
			}
		}
		cur = p.Next
		if !p.More {
			break
		}
	}
	if fmt.Sprint(seqs) != "[1 2 3 4 5 6 7 8 9 10]" || cur.Seq != 10 {
		t.Fatalf("seqs %v, cursor %d", seqs, cur.Seq)
	}
}

// TestCursorGap is row cursor_below_retention_gap: with the rows below seq 6 deleted, a cursor below the oldest retained seq answers a
// gap, one at the edge reads on, and one beyond the store is refused.
func TestCursorGap(t *testing.T) {
	s, cur := feedFixture(t, 10)
	rawExec(t, s, `DELETE FROM _feed WHERE seq <= 5`)
	for _, seq := range []uint64{0, 3, 4} {
		cur.Seq = seq
		if _, err := s.Feed(bg, cur, 10); !errors.Is(err, ErrGap) || errors.Is(err, ErrLineage) {
			t.Fatalf("cursor %d: %v", seq, err)
		}
	}
	cur.Seq = 5
	p, err := s.Feed(bg, cur, 10)
	if err != nil || len(p.Changes) != 5 || p.Changes[0].Seq != 6 {
		t.Fatalf("cursor 5: %+v %v", p, err)
	}
	cur.Seq = 11
	if _, err := s.Feed(bg, cur, 10); !errors.Is(err, ErrCursorAhead) {
		t.Fatalf("cursor 11: %v", err)
	}
	rawExec(t, s, `DELETE FROM _feed`)
	cur.Seq = 9
	if _, err := s.Feed(bg, cur, 10); !errors.Is(err, ErrGap) {
		t.Fatalf("an empty feed below the position: %v", err)
	}
}

// TestCursorWrongLineage is row cursor_wrong_lineage: a cursor of another lineage or another store is a gap whose cause is
// the lineage.
func TestCursorWrongLineage(t *testing.T) {
	s, cur := feedFixture(t, 3)
	other := cur
	other.Lineage[0] ^= 0xff
	if _, err := s.Feed(bg, other, 10); !errors.Is(err, ErrGap) || !errors.Is(err, ErrLineage) {
		t.Fatalf("another lineage: %v", err)
	}
	other = cur
	other.StoreID = mkID(t, "sto", 99)
	if _, err := s.Feed(bg, other, 10); !errors.Is(err, ErrGap) || !errors.Is(err, ErrLineage) {
		t.Fatalf("another store: %v", err)
	}
	if p, err := s.Feed(bg, cur, 10); err != nil || len(p.Changes) != 3 {
		t.Fatalf("the right cursor: %+v %v", p, err)
	}
}

// ---- scan ----------------------------------------------------------------------------------------------------

// TestScanPaging writes 1,000 shuffled ids and reads them in pages of 7: every id once, in byte order, which is
// text order.
func TestScanPaging(t *testing.T) {
	s, _ := newStore(t, D1)
	nums := rand.New(rand.NewSource(7)).Perm(1000)
	for _, n := range nums {
		if res, err := s.Insert(bg, mkID(t, "itm", n+1), []byte(fmt.Sprint(n))); err != nil || res.Outcome != Committed {
			t.Fatal(res, err)
		}
	}
	var got []string
	var ids [][]byte
	var after [16]byte
	for pages := 0; ; pages++ {
		p, err := s.Scan(bg, after, 7)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range p.Items {
			got = append(got, it.ID.String())
			ids = append(ids, it.ID.Bytes())
		}
		if !p.More {
			if len(p.Items) == 0 && pages == 0 {
				t.Fatal("no items")
			}
			break
		}
		if len(p.Items) != 7 || p.Next != p.Items[6].ID {
			t.Fatalf("page %d: %d items, next %v", pages, len(p.Items), p.Next)
		}
		after = p.Next
	}
	if len(got) != 1000 {
		t.Fatalf("scanned %d items, want 1000", len(got))
	}
	if !sort.SliceIsSorted(ids, func(i, j int) bool { return bytes.Compare(ids[i], ids[j]) < 0 }) {
		t.Fatal("ids not in byte order")
	}
	if !sort.StringsAreSorted(got) {
		t.Fatal("ids not in text order")
	}
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Fatalf("id %s twice", got[i])
		}
	}
}

func TestScanLimitBounds(t *testing.T) {
	s, _ := newStore(t, D1)
	s.Insert(bg, mkID(t, "itm", 1), []byte("a"))
	for _, limit := range []int{-1, 0, 1001} {
		if _, err := s.Scan(bg, [16]byte{}, limit); !errors.Is(err, ErrLimit) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	for _, limit := range []int{1, 1000} {
		p, err := s.Scan(bg, [16]byte{}, limit)
		if err != nil || len(p.Items) != 1 || p.More {
			t.Fatalf("limit %d: %+v %v", limit, p, err)
		}
	}
}

// TestClosed: a closed store refuses every call, and Close is idempotent.
func TestClosed(t *testing.T) {
	s, path := newStore(t, D1)
	if err := s.Close(); err != nil || s.Close() != nil {
		t.Fatal("Close")
	}
	if _, err := s.Insert(bg, mkID(t, "itm", 1), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Insert: %v", err)
	}
	if _, _, err := s.Get(bg, mkID(t, "itm", 1)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get: %v", err)
	}
	if err := s.Health(bg); !errors.Is(err, ErrClosed) {
		t.Fatalf("Health: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	s2.Close()
}

// TestEmptyValueAndBadKey: an empty value is stored, the zero id is not a key.
func TestEmptyValueAndBadKey(t *testing.T) {
	s, _ := newStore(t, D1)
	id := mkID(t, "itm", 1)
	if res, err := s.Insert(bg, id, nil); err != nil || res.Outcome != Committed {
		t.Fatalf("empty value: %v %v", res, err)
	}
	if it, found, err := s.Get(bg, id); err != nil || !found || len(it.Value) != 0 {
		t.Fatalf("read back: %+v %v %v", it, found, err)
	}
	if res, err := s.Insert(bg, [16]byte{}, []byte("x")); !errors.Is(err, ErrRefused) || !errors.Is(err, ErrBadKey) || res.Outcome != Refused {
		t.Fatalf("zero id: %v %v", res, err)
	}
}

// TestCancelledContext: a context that is done before the write refuses it, and nothing is written.
func TestCancelledContext(t *testing.T) {
	s, _ := newStore(t, D1)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	res, err := s.Insert(ctx, mkID(t, "itm", 1), []byte("x"))
	if res.Outcome != Refused || !errors.Is(err, ErrRefused) || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v %v", res, err)
	}
	if got := rawQuery(t, s, `SELECT count(*) FROM item`); got[0] != "0" {
		t.Fatalf("rows %v", got)
	}
}
