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
//	                                            | Rejected: bytes reason | OutcomeUnknown: bytes reason
//	snapshot  -                                 Ok: u64 commit_seq
//	get       bytes db, bytes key, u64 at       Ok: u8 found, [bytes value]
//	scan      bytes db, bytes after, u32 limit, u64 at
//	                                            Ok: u64 at, u32 n, n x (bytes key, bytes value), u8 more, bytes next_after
//	health    -                                 Ok
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
// Reads are Get, Snapshot and Scan. Scans continue "after K" (exclusive) and
// carry the commit sequence they read at, so a caller pins the first page's At
// on the following pages; ScanAll does this.
package mxdb
