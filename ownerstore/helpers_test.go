// SPDX-License-Identifier: MIT

package ownerstore

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fiberfx/mx-echo/v7/reqid"
)

// mkID returns the Ecd ID of the given kind (three letters) and number n: ids with a larger n sort later.
func mkID(t testing.TB, kind string, n int) reqid.EID {
	t.Helper()
	sys, ok := reqid.ParseCode("mxe")
	if !ok {
		t.Fatal("system code")
	}
	k, ok := reqid.ParseCode(kind)
	if !ok {
		t.Fatalf("kind %q", kind)
	}
	id, err := reqid.EIDFromParts(sys, k, 1_700_000_000_000+uint64(n/200000), 1, uint32(n%200000))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// newStore creates and opens a store of the class in a temporary directory.
func newStore(t testing.TB, class Class) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	if err := Create(path, mkID(t, "sto", 1), class); err != nil {
		t.Fatalf("Create: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

// fakeErr is an error that carries a SQLite result code, as the driver's errors do.
type fakeErr struct{ code int }

func (e *fakeErr) Error() string { return fmt.Sprintf("injected sqlite error, code %d", e.code) }
func (e *fakeErr) Code() int     { return e.code }

// Result codes the tests inject: SQLITE_IOERR_FSYNC, SQLITE_FULL, SQLITE_BUSY, SQLITE_IOERR.
const (
	codeIOFsync = 10 | 4<<8
	codeFull    = 13
	codeBusy    = 5
	codeIOErr   = 10
)

// commitThenFail is a commit hook: the COMMIT happens, then it answers err, as when the reply is lost.
func commitThenFail(err error) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		if cerr := tx.Commit(); cerr != nil {
			return cerr
		}
		return err
	}
}

// rollbackThenFail is a commit hook: the transaction is rolled back, then it answers err.
func rollbackThenFail(err error) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		_ = tx.Rollback()
		return err
	}
}

// leaveOpenAndFail is a commit hook: it answers err and leaves the transaction open.
func leaveOpenAndFail(err error) func(*sql.Tx) error {
	return func(*sql.Tx) error { return err }
}

// rawQuery runs a query on the store's writer connection and returns its first row as strings.
func rawQuery(t testing.TB, s *Store, query string, args ...any) []string {
	t.Helper()
	conn, err := s.w.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	if rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			out = append(out, fmt.Sprint(v))
		}
	}
	return out
}

// rawExec runs a statement on the writer connection.
func rawExec(t testing.TB, s *Store, query string, args ...any) {
	t.Helper()
	conn, err := s.w.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// rawReader opens an independent read-only connection on the file, which clears nothing on the store's handle.
func rawReader(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", pageURI(path, "rw", "busy_timeout(2000)", "query_only(1)"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// ---- child processes -----------------------------------------------------------------------------------------

// The test binary re-executes itself as a child: TestChildProcess does nothing unless the environment says so.
const (
	envChild = "OWNERSTORE_TEST_CHILD"
	envPath  = "OWNERSTORE_TEST_PATH"
	envFirst = "OWNERSTORE_TEST_FIRST"
	envCount = "OWNERSTORE_TEST_COUNT"
)

// TestChildProcess is the body of a child process. Mode "write" opens the store, inserts COUNT items numbered from
// FIRST, prints "ACK n" after each returned write and then waits to be killed; mode "hold" opens the store, prints
// "HELD" and waits.
func TestChildProcess(t *testing.T) {
	mode := os.Getenv(envChild)
	if mode == "" {
		t.Skip("not a child process")
	}
	s, err := Open(os.Getenv(envPath))
	if err != nil {
		fmt.Println("OPENFAIL", err)
		os.Exit(3)
	}
	switch mode {
	case "hold":
		fmt.Println("HELD")
	case "write":
		first, _ := strconv.Atoi(os.Getenv(envFirst))
		count, _ := strconv.Atoi(os.Getenv(envCount))
		for i := 0; i < count; i++ {
			res, err := s.Insert(context.Background(), mkID(t, "itm", first+i), []byte(fmt.Sprintf("value-%d", first+i)))
			if err != nil || res.Outcome != Committed {
				fmt.Println("WRITEFAIL", err)
				os.Exit(4)
			}
			fmt.Println("ACK", first+i)
		}
	}
	time.Sleep(time.Minute)
	s.Close()
}

// child is a running child process.
type child struct {
	cmd *exec.Cmd
	out *bufio.Scanner
}

func startChild(t testing.TB, mode, path string, first, count int) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcess$", "-test.v=false")
	cmd.Env = append(os.Environ(), envChild+"="+mode, envPath+"="+path, envFirst+"="+strconv.Itoa(first), envCount+"="+strconv.Itoa(count))
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, out: bufio.NewScanner(pipe)}
	t.Cleanup(func() { c.kill() })
	return c
}

// waitLine reads the child's output until a line has the prefix and returns it.
func (c *child) waitLine(t testing.TB, prefix string) string {
	t.Helper()
	for c.out.Scan() {
		if line := c.out.Text(); strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("child ended without a line starting %q", prefix)
	return ""
}

// kill sends SIGKILL (the process is killed outright, nothing is flushed or closed) and waits for it.
func (c *child) kill() {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
		c.cmd.Process = nil
	}
}
