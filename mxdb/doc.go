// Package mxdb is the Go client of mxdbd, the mxdb sidecar (protocol version 1).
//
// It uses the standard library only. One request is one TCP round trip on a
// pooled connection: a frame is a u32 big-endian body length (1 to MaxFrame)
// followed by the body; a request body is an operation byte followed by its
// fields, a reply body is a status byte followed by its fields. A field is a
// u8, a big-endian u32 or u64, or bytes (a u32 length, then the bytes). The
// first frame on a connection is hello {u32 version}.
//
//	hello     u32 version                       Ok: u32 version
//	update    dbs, pre, ops (see Update)        Ok: u64 commit_seq | Aborted: u32 failed pre index
//	                                            | Rejected: u8 code, bytes reason | OutcomeUnknown: bytes reason
//	snapshot  -                                 Ok: u64 commit_seq
//	get       bytes db, bytes key, u64 at       Ok: u8 found, [bytes value]
//	scan      bytes db, bytes after, u32 limit, u64 at
//	                                            Ok: u64 at, u32 n, n x (bytes key, bytes value), u8 more, bytes next_after
//	health    -                                 Ok
//
// Any request may also be answered with status 4, ProtocolError {bytes reason}:
// the server could not parse the request and closes the connection. Statuses
// are Ok 0, Aborted 1, Rejected 2, OutcomeUnknown 3, ProtocolError 4. The
// Rejected code is one byte, numbered from 0: KeyTooLong, ValueTooLong,
// TransactionTooLarge, Poisoned, Io, LockLost, NoSuchCommit, DamagedTail,
// Other; RejectedError.Code carries it.
//
// The wire is declarative: an update declares the databases it touches, a list
// of preconditions (key holds value, key absent) and a list of operations (put,
// delete, delete_range over [from, to)); the server evaluates the preconditions
// inside the transaction. There is no read-then-decide across the socket.
//
// The four outcomes of an update are distinct. Update returns a Result whose
// Outcome is OutcomeOk, OutcomeAborted, OutcomeRejected or OutcomeUnknown, and
// an error that is nil for Ok and otherwise one of AbortedError, RejectedError
// or UnknownError, matched with errors.Is against ErrAborted, ErrRejected and
// ErrOutcomeUnknown. OutcomeUnknown means the request may or may not have been
// committed (the connection dropped, the call timed out or was cancelled after
// a byte was sent, the reply was malformed, or the server said so): read back
// before retrying. A request that provably never left the client is a
// NotSentError (ErrNotSent) and is safe to retry.
//
// A ProtocolError reply is, for an update, Rejected with nothing committed;
// the error is a *ProtocolError, which matches ErrProtocol and ErrRejected, and
// the client closes that connection instead of pooling it.
//
// Retry on a stale connection: when a pooled connection fails before any byte
// of the reply arrives (reset, closed by the server while idle, nothing
// written), the client sends the request once more on a fresh connection, for
// updates as for reads. A failure on that fresh connection, a failure after
// the first reply byte, a timeout and a cancelled context are not retried:
// an update is OutcomeUnknown. Because the first send may have reached the
// server, a caller whose update is not idempotent should carry a precondition
// (Absent, Holds) that makes a repeated execution Aborted.
//
// Reads are Get, Snapshot and Scan. Scans continue "after K" (exclusive) and
// carry the commit sequence they read at, so a caller pins the first page's At
// on the following pages; ScanAll does this. When the server no longer has the
// pinned commit (Rejected, NoSuchCommit) Scan returns that error and ScanAll
// reads the page again at At 0 and pins to the new commit.
package mxdb
