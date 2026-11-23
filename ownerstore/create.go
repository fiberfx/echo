// SPDX-License-Identifier: MIT

package ownerstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

// schemaDDL is schema 1. There is no secondary index: keys are Ecd IDs, so a table of one kind grows at its right
// edge, and the byte order of the key is its text order.
const schemaDDL = `
CREATE TABLE _store (one INTEGER PRIMARY KEY CHECK (one = 1), store_id BLOB NOT NULL, lineage BLOB NOT NULL,
  schema INTEGER NOT NULL, durability TEXT NOT NULL, seq INTEGER NOT NULL);
CREATE TABLE item (id BLOB PRIMARY KEY CHECK (typeof(id) = 'blob' AND length(id) = 16), rev INTEGER NOT NULL,
  value BLOB NOT NULL CHECK (length(value) <= 65536), seq INTEGER NOT NULL) WITHOUT ROWID;
CREATE TABLE _feed (seq INTEGER NOT NULL, tbl TEXT NOT NULL, key BLOB NOT NULL, op INTEGER NOT NULL,
  PRIMARY KEY (seq, tbl, key)) WITHOUT ROWID;
`

// testCreateHook, when set by a test, is called by Create with a stage name and aborts it with the error it
// returns. The stage "synced" comes after the new file is complete and synced, before it takes its final name.
var testCreateHook func(stage string) error

// Create writes a new store at path, with the store id and the durability class recorded in it. The file is built
// under a temporary name in the same directory, synced, given its final name without replacing anything, and the
// directory is synced; a failed Create leaves nothing under the final name. A store that exists is ErrExists. The
// directory must exist. storeID is an Ecd ID of kind sto.
func Create(path string, storeID reqid.EID, class Class) error {
	if class != D1 && class != D2 {
		return fmt.Errorf("%w: %q", ErrClass, class)
	}
	if _, err := reqid.EIDFromBytes(storeID[:]); err != nil || storeID.Kind().String() != "sto" {
		return ErrStoreID
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, abs)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := abs + ".creating-" + hex.EncodeToString(nonce[:])
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			_ = os.Remove(tmp + suffix)
		}
	}()
	if err := buildStore(tmp, storeID, class); err != nil {
		return err
	}
	if err := syncFile(tmp); err != nil {
		return err
	}
	if testCreateHook != nil {
		if err := testCreateHook("synced"); err != nil {
			return err
		}
	}
	if err := os.Link(tmp, abs); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrExists, abs)
		}
		return err
	}
	if err := os.Remove(tmp); err != nil {
		return err
	}
	return syncDir(filepath.Dir(abs))
}

// buildStore writes the whole store at path, checkpoints its log into the file, and closes it.
func buildStore(path string, storeID reqid.EID, class Class) error {
	ctx := context.Background()
	db, err := openDB(pageURI(path, "rwc", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS)), 1)
	if err != nil {
		return err
	}
	defer db.Close()
	// auto_vacuum is fixed by the first table, so it comes first; journal_mode=WAL is stored in the file.
	if _, err := db.ExecContext(ctx, "PRAGMA auto_vacuum = INCREMENTAL"); err != nil {
		return err
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return fmt.Errorf("ownerstore: journal_mode is %q after asking for wal", mode)
	}
	var lineage [16]byte
	if _, err := rand.Read(lineage[:]); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, schemaDDL); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO _store (one, store_id, lineage, schema, durability, seq) VALUES (1, ?1, ?2, ?3, ?4, 0)`,
		storeID[:], lineage[:], schemaVersion, string(class)); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	var busy, logFrames, ckpt int
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(FULL)").Scan(&busy, &logFrames, &ckpt); err != nil {
		return err
	}
	if busy != 0 || logFrames != ckpt {
		return fmt.Errorf("ownerstore: checkpoint of the new store incomplete (busy %d, log %d, checkpointed %d)", busy, logFrames, ckpt)
	}
	if err := db.Close(); err != nil {
		return err
	}
	if st, err := os.Stat(path + "-wal"); err == nil && st.Size() != 0 {
		return fmt.Errorf("ownerstore: the new store left a log of %d bytes", st.Size())
	}
	return nil
}

// syncFile flushes the file at path.
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
