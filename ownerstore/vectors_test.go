// SPDX-License-Identifier: MIT

package ownerstore

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v7/reqid"
)

// readTSV reads a vector file of testdata/contract: lines that start with # and empty lines are skipped, the first
// other line names the columns, and each following line is one row with exactly that many tab-separated fields.
func readTSV(t testing.TB, name string) []map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "contract", name))
	if err != nil {
		t.Fatal(err)
	}
	var cols []string
	var rows []map[string]string
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if cols == nil {
			cols = f
			continue
		}
		if len(f) != len(cols) {
			t.Fatalf("%s: %d fields, want %d: %q", name, len(f), len(cols), line)
		}
		row := map[string]string{}
		for i, c := range cols {
			row[c] = f[i]
		}
		rows = append(rows, row)
	}
	return rows
}

// vstep is one row of outcomes.tsv.
type vstep struct {
	caze, req, key, pre, fault, want, readback string
	n, size                                    int
}

func parseSteps(t testing.TB) (cases []string, byCase map[string][]vstep) {
	byCase = map[string][]vstep{}
	for _, r := range readTSV(t, "outcomes.tsv") {
		st := vstep{caze: r["case"], req: r["request"], key: r["key"], pre: r["precondition"], fault: r["fault"],
			want: r["want_outcome"], readback: r["want_readback"], size: -1}
		var err error
		if st.n, err = strconv.Atoi(r["step"]); err != nil {
			t.Fatal(err)
		}
		if r["size"] != "-" {
			if st.size, err = strconv.Atoi(r["size"]); err != nil {
				t.Fatal(err)
			}
		}
		if _, ok := byCase[st.caze]; !ok {
			cases = append(cases, st.caze)
		}
		byCase[st.caze] = append(byCase[st.caze], st)
	}
	return cases, byCase
}

// oneOf reports whether got is one of the alternatives of want ("a or b").
func oneOf(want, got string) bool {
	for _, alt := range strings.Split(want, " or ") {
		if alt == got {
			return true
		}
	}
	return false
}

// harness runs the steps of one case on a fresh store.
type harness struct {
	t        *testing.T
	s        *Store
	path     string
	raw      *sql.DB
	variant  string // how an io-at-commit fault lands: "commit" (COMMIT, then the error) or "rollback" (rolled back, then the error)
	injected error
	filler   uint64
}

// state reads the key's durable state through a connection of its own, so that it does not touch the handle's poison.
func (h *harness) state(id reqid.EID) string {
	var rev int64
	err := h.raw.QueryRow(`SELECT rev FROM item WHERE id = ?1`, id[:]).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return "absent"
	}
	if err != nil {
		h.t.Fatalf("read-back: %v", err)
	}
	return fmt.Sprintf("rev=%d", rev)
}

// apply injects the fault before a step and returns what undoes it after.
func (h *harness) apply(fault string) func() {
	t := h.t
	switch fault {
	case "-":
		return func() {}
	case "io-at-commit":
		h.injected = &fakeErr{codeIOFsync}
		if h.variant == "commit" {
			h.s.hooks.commit = commitThenFail(h.injected)
		} else {
			h.s.hooks.commit = rollbackThenFail(h.injected)
		}
		return func() { h.s.hooks.commit = nil }
	case "full-open":
		h.s.hooks.commit = leaveOpenAndFail(&fakeErr{codeFull})
		return func() { h.s.hooks.commit = nil }
	case "full-rolled-back":
		h.fillToFull()
		return func() {}
	case "space-freed":
		rawExec(t, h.s, "PRAGMA max_page_count = 1073741823")
		return func() {}
	case "busy":
		db, err := sql.Open("sqlite", pageURI(h.path, "rw", "busy_timeout(100)")+"&_txlock=immediate")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		tx, err := db.BeginTx(bg, nil)
		if err != nil {
			t.Fatalf("the second connection could not take the write lock: %v", err)
		}
		return func() { tx.Rollback(); db.Close() }
	}
	t.Fatalf("unknown fault %q", fault)
	return nil
}

// fillToFull limits the file to a few more pages and writes small rows, in ascending key order below every Ecd ID,
// until SQLite answers SQLITE_FULL: the rightmost leaf of item is then too full for the next row, which is a
// 100-byte value under a key above the fillers.
func (h *harness) fillToFull() {
	t := h.t
	conn, err := h.s.w.Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var pages int
	if err := conn.QueryRowContext(bg, "PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(bg, fmt.Sprintf("PRAGMA max_page_count = %d", pages+2)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500000; i++ {
		h.filler++
		id := make([]byte, 16)
		binary.BigEndian.PutUint64(id[8:], h.filler)
		_, err := conn.ExecContext(bg, `INSERT INTO item (id, rev, value, seq) VALUES (?1, 1, x'00', 0)`, id)
		if err == nil {
			continue
		}
		if sqliteCode(err) != sqliteFull {
			t.Fatalf("filler %d: %v", i, err)
		}
		return
	}
	t.Fatal("the file never filled")
}

// outcomeOf names an outcome the way the vectors do.
func outcomeOf(res Result, err error) string {
	if (err == nil) != (res.Outcome == Committed) {
		return fmt.Sprintf("inconsistent (%v, %v)", res.Outcome, err)
	}
	return res.Outcome.String()
}

// run executes the steps and returns how many it checked.
func (h *harness) run(steps []vstep) int {
	t := h.t
	ids := map[string]reqid.EID{}
	known := map[string][]byte{}   // a key's value where the harness knows it
	landing := map[string]string{} // what an io-at-commit step left behind, as this variant forces it
	lastGet := map[string]string{} // "found" or "absent": the last get of a key
	checked := 0
	for _, st := range steps {
		id, ok := ids[st.key]
		if !ok {
			id = mkID(t, "itm", len(ids)+1)
			ids[st.key] = id
		}
		label := fmt.Sprintf("%s step %d (%s %s, fault %s)", st.caze, st.n, st.req, st.key, st.fault)
		value := bytes.Repeat([]byte{byte(len(st.key) + st.n)}, max(st.size, 0))
		if st.req == "create" {
			value = bytes.Repeat([]byte{byte(id[15])}, st.size)
		}
		// A create that waits on a read-back ("committed or conflict" after an unknown outcome, with no get step of its
		// own in the vector) follows the contract's rule: read back by the same id, which reopens the handle, then retry.
		if st.req == "create" && st.fault == "-" && strings.Contains(st.want, " or ") && h.s.poisoned() != nil {
			_, found, gerr := h.s.Get(bg, id)
			if gerr != nil {
				t.Fatalf("%s: read-back before the retry: %v", label, gerr)
			}
			lastGet[st.key] = "absent"
			if found {
				lastGet[st.key] = "found"
			}
			t.Logf("%s: read back by the same id before the retry (the vector has no get step here)", label)
		}
		undo := h.apply(st.fault)
		poisonedBefore := h.s.poisoned() != nil
		reopensBefore := h.s.Report().Reopens
		var (
			res  Result
			err  error
			got  string
			item Item
		)
		switch st.req {
		case "create":
			res, err = h.s.Insert(bg, id, value)
			got = outcomeOf(res, err)
		case "update":
			rev, perr := strconv.ParseUint(strings.TrimPrefix(st.pre, "rev="), 10, 64)
			if perr != nil {
				t.Fatalf("%s: precondition %q", label, st.pre)
			}
			res, err = h.s.Update(bg, id, rev, value)
			got = outcomeOf(res, err)
		case "get":
			var found bool
			item, found, err = h.s.Get(bg, id)
			if err != nil {
				t.Fatalf("%s: Get: %v", label, err)
			}
			got = "absent"
			if found {
				got = "found"
			}
			lastGet[st.key] = got
		default:
			t.Fatalf("%s: unknown request", label)
		}
		undo()

		// The outcome.
		wantOutcome, cause, _ := strings.Cut(st.want, ":")
		if !oneOf(wantOutcome, got) {
			t.Fatalf("%s: outcome %s (%v), want %s", label, got, err, st.want)
		}
		switch got {
		case "conflict":
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("%s: conflict without ErrConflict: %v", label, err)
			}
			if v, ok := known[st.key]; ok && res.Stored != nil && !bytes.Equal(res.Stored, v) {
				t.Fatalf("%s: the conflict carries %d bytes, the stored value has %d", label, len(res.Stored), len(v))
			}
		case "refused":
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("%s: refused without ErrRefused: %v", label, err)
			}
			if st.size > MaxValue && !errors.Is(err, ErrTooLarge) {
				t.Fatalf("%s: an oversized value is refused for another cause: %v", label, err)
			}
		case "unknown":
			if !errors.Is(err, ErrUnknown) {
				t.Fatalf("%s: unknown without ErrUnknown: %v", label, err)
			}
		}
		switch st.fault {
		case "io-at-commit":
			if got != "unknown" || !errors.Is(err, h.injected) || h.s.poisoned() == nil {
				t.Fatalf("%s: err %v, poisoned %v", label, err, h.s.poisoned())
			}
			landing[st.key] = "absent"
			if h.variant == "commit" {
				landing[st.key] = "rev=1"
			}
		case "full-open":
			if got != "unknown" || h.s.poisoned() == nil {
				t.Fatalf("%s: err %v, poisoned %v", label, err, h.s.poisoned())
			}
			landing[st.key] = "absent"
		case "full-rolled-back":
			if sqliteCode(err) != sqliteFull || h.s.poisoned() != nil {
				t.Fatalf("%s: err %v, poisoned %v: SQLITE_FULL must refuse without poisoning", label, err, h.s.poisoned())
			}
		case "busy":
			if !errors.Is(err, ErrBusy) || h.s.poisoned() != nil {
				t.Fatalf("%s: err %v, poisoned %v", label, err, h.s.poisoned())
			}
		}
		if cause == "first-cause" {
			if !errors.Is(err, ErrPoisoned) || !errors.Is(err, h.injected) {
				t.Fatalf("%s: the refusal does not carry the cause that poisoned the handle: %v", label, err)
			}
		}
		if st.req == "get" && poisonedBefore {
			if h.s.poisoned() != nil || h.s.Report().Reopens != reopensBefore+1 {
				t.Fatalf("%s: a read on a poisoned handle must reopen it (poisoned %v, reopens %d -> %d)", label, h.s.poisoned(), reopensBefore, h.s.Report().Reopens)
			}
		}

		// Whether a retry after a get commits or conflicts follows the get.
		if st.req == "create" && st.fault == "-" && strings.Contains(st.want, " or ") && lastGet[st.key] != "" {
			wantNow := "committed"
			if lastGet[st.key] == "found" {
				wantNow = "conflict"
			}
			if got != wantNow {
				t.Fatalf("%s: after a read-back of %s the retry answered %s, want %s", label, lastGet[st.key], got, wantNow)
			}
		}

		// The key's stored state after the step.
		var state string
		switch {
		case st.readback == "-":
		case st.req == "get":
			state = "absent"
			if got == "found" {
				state = fmt.Sprintf("rev=%d", item.Rev)
			}
		default:
			state = h.state(id)
		}
		if st.readback != "-" {
			if !oneOf(st.readback, state) {
				t.Fatalf("%s: stored state %s, want %s", label, state, st.readback)
			}
			if want, ok := landing[st.key]; ok && st.fault != "-" || ok && st.req == "get" {
				if state != want {
					t.Fatalf("%s: stored state %s, but this fault lands as %s", label, state, want)
				}
			}
		}
		if st.req == "get" && got == "found" && st.size == -1 {
			if v, ok := known[st.key]; ok && !bytes.Equal(item.Value, v) {
				t.Fatalf("%s: the value read has %d bytes, the stored one %d", label, len(item.Value), len(v))
			}
		}
		if got == "committed" {
			known[st.key] = value
			delete(landing, st.key)
		}
		if got == "conflict" && st.req == "create" {
			delete(landing, st.key)
		}
		checked++
	}
	return checked
}

// TestOutcomeVectors runs every step of outcomes.tsv through the adapter: the request, the key, the precondition, the
// size and the fault in; the outcome and the read-back out. It fails unless every step was checked. A case with an
// io-at-commit fault runs twice, once with the COMMIT landing before the error and once rolled back; the vector accepts
// both landings, and each run also holds the read-back to the one its variant forces.
func TestOutcomeVectors(t *testing.T) {
	cases, byCase := parseSteps(t)
	total, checked := 0, 0
	for _, c := range cases {
		total += len(byCase[c])
	}
	if len(cases) != 13 || total != 34 {
		t.Fatalf("outcomes.tsv has %d cases and %d steps, want 13 and 34", len(cases), total)
	}
	for _, c := range cases {
		steps := byCase[c]
		variants := []string{""}
		for _, st := range steps {
			if st.fault == "io-at-commit" {
				variants = []string{"commit", "rollback"}
			}
		}
		for i, v := range variants {
			t.Run(strings.TrimSuffix(c+"-"+v, "-"), func(t *testing.T) {
				s, path := newStore(t, D2)
				h := &harness{t: t, s: s, path: path, raw: rawReader(t, path), variant: v}
				n := h.run(steps)
				if i == 0 {
					checked += n
				}
			})
		}
	}
	if checked != total {
		t.Fatalf("checked %d of %d steps", checked, total)
	}
}

// rowTests maps each required check of rows.tsv to the test that holds it (a subtest after a slash).
var rowTests = map[string]string{
	"settings_equal_class_d1":      "TestClassReadBack/D1",
	"settings_equal_class_d2":      "TestClassReadBack/D2",
	"settings_mismatch_refuses":    "TestClassPlant",
	"declared_class_reported":      "TestDeclaredClassReported",
	"d1_survives_kill9":            "TestKillKeepsWrite/D1",
	"d2_ack_after_sync":            "TestD2NoAckAfterFailedSync",
	"open_missing_refuses":         "TestOpenMissing",
	"open_does_not_migrate":        "TestOpenDoesNotMigrate",
	"create_failed_leaves_none":    "TestCreateFailedLeavesNone",
	"second_writer_refused":        "TestSecondOwner",
	"recovery_reports_position":    "TestRecoveryReport",
	"recovery_torn_tail_discarded": "TestRecoveryTornTail",
	"seq_no_gap_10000":             "TestSeqContiguous",
	"feed_row_in_transaction":      "TestFeedRowInTransaction",
	"cursor_below_retention_gap":   "TestCursorGap",
	"cursor_wrong_lineage":         "TestCursorWrongLineage",
}

// TestRowsMapping: every check of rows.tsv whose status is required maps to a test of this package, a deferred check
// maps to none, and nothing else is mapped.
func TestRowsMapping(t *testing.T) {
	required := map[string]bool{}
	for _, r := range readTSV(t, "rows.tsv") {
		switch {
		case r["status"] == "required":
			required[r["check"]] = true
		case strings.HasPrefix(r["status"], "deferred"):
			if _, ok := rowTests[r["check"]]; ok {
				t.Errorf("deferred check %s is mapped", r["check"])
			}
		default:
			t.Errorf("check %s has status %q", r["check"], r["status"])
		}
	}
	if len(required) != 16 {
		t.Errorf("rows.tsv has %d required checks, want 16", len(required))
	}
	files, _ := filepath.Glob("*_test.go")
	var src []byte
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, b...)
	}
	for check := range required {
		name, ok := rowTests[check]
		if !ok {
			t.Errorf("required check %s has no test", check)
			continue
		}
		fn, _, _ := strings.Cut(name, "/")
		if !regexp.MustCompile(`(?m)^func ` + fn + `\(t \*testing\.T\)`).Match(src) {
			t.Errorf("check %s maps to %s, which is not a test of this package", check, fn)
		}
	}
	for check := range rowTests {
		if !required[check] {
			t.Errorf("%s is mapped but is not a required check of rows.tsv", check)
		}
	}
}
