package mxdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// Outcome is the result class of an update.
type Outcome uint8

const (
	// OutcomeNone is the zero value: no reply, the request was not sent.
	OutcomeNone Outcome = iota
	OutcomeOk
	OutcomeAborted
	OutcomeRejected
	OutcomeUnknown
)

func (o Outcome) String() string {
	switch o {
	case OutcomeOk:
		return "Ok"
	case OutcomeAborted:
		return "Aborted"
	case OutcomeRejected:
		return "Rejected"
	case OutcomeUnknown:
		return "OutcomeUnknown"
	}
	return "None"
}

// Result describes one update. CommitSeq is set for Ok, FailedPre for Aborted,
// Code and Reason for Rejected, Reason for an Unknown the server reported.
type Result struct {
	Outcome   Outcome
	CommitSeq uint64
	FailedPre int
	Code      RejectCode
	Reason    string
}

// Cond is a precondition checked inside the update's transaction.
type Cond struct {
	db, key string
	value   []byte
	absent  bool
}

// Holds requires key in db to hold exactly value.
func Holds(db, key string, value []byte) Cond { return Cond{db: db, key: key, value: value} }

// Absent requires key in db to be absent.
func Absent(db, key string) Cond { return Cond{db: db, key: key, absent: true} }

// Op is one write of an update.
type Op struct {
	kind    byte
	db, key string
	value   []byte
	end     string
}

// Put sets key in db to value.
func Put(db, key string, value []byte) Op { return Op{kind: kindPut, db: db, key: key, value: value} }

// Delete removes key from db.
func Delete(db, key string) Op { return Op{kind: kindDelete, db: db, key: key} }

// DeleteRange removes the keys in [from, to) from db.
func DeleteRange(db, from, to string) Op {
	return Op{kind: kindDeleteRange, db: db, key: from, end: to}
}

// Update is a declarative transaction: every Cond in Pre must hold, then all
// Ops apply, atomically. The databases it touches are those its Pre and Ops
// name, in order of first appearance.
type Update struct {
	Pre []Cond
	Ops []Op
}

// Config configures a Client.
type Config struct {
	Addr    string                                      // host:port of mxdbd, used when Dial is nil
	Dial    func(ctx context.Context) (net.Conn, error) // replaces the TCP dial
	MaxIdle int                                         // idle connections kept; default 4
	Timeout time.Duration                               // per call, when ctx has no earlier deadline; default 10s
}

// Client is safe for concurrent use. Each in-flight call owns one connection.
type Client struct {
	cfg    Config
	mu     sync.Mutex
	idle   []net.Conn
	closed bool
}

// New returns a Client; it dials on first use.
func New(cfg Config) *Client {
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = 4
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Dial == nil {
		addr := cfg.Addr
		cfg.Dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}
	}
	return &Client{cfg: cfg}
}

// Close closes the idle connections; later calls fail with ErrNotSent.
func (c *Client) Close() error {
	c.mu.Lock()
	idle := c.idle
	c.idle, c.closed = nil, true
	c.mu.Unlock()
	for _, cn := range idle {
		_ = cn.Close()
	}
	return nil
}

// Update runs u. The error is nil only for OutcomeOk; otherwise it is an
// *AbortedError, *RejectedError, *UnknownError or *NotSentError (see the
// package comment). After OutcomeUnknown read back before retrying.
func (c *Client) Update(ctx context.Context, u Update) (Result, error) {
	body, err := encodeUpdate(u)
	if err != nil {
		return Result{}, &NotSentError{Err: err}
	}
	reply, sent, err := c.do(ctx, body, false)
	if err != nil {
		if !sent {
			return Result{}, &NotSentError{Err: err}
		}
		return Result{Outcome: OutcomeUnknown}, unknownErr(err)
	}
	res, err := decodeUpdateReply(reply)
	if err != nil && res.Outcome == OutcomeNone {
		return Result{Outcome: OutcomeUnknown}, unknownErr(err)
	}
	return res, err
}

// Snapshot returns the latest commit sequence.
func (c *Client) Snapshot(ctx context.Context) (uint64, error) {
	d, err := c.read(ctx, []byte{opSnapshot})
	if err != nil {
		return 0, err
	}
	seq := d.u64()
	return seq, d.finish()
}

// Get reads key in db as of commit sequence at (0 reads the latest).
func (c *Client) Get(ctx context.Context, db, key string, at uint64) (value []byte, found bool, err error) {
	if db == "" || key == "" {
		return nil, false, &NotSentError{Err: ErrEmptyName}
	}
	e := enc{b: []byte{opGet}}
	e.str(db)
	e.str(key)
	e.u64(at)
	d, err := c.read(ctx, e.b)
	if err != nil {
		return nil, false, err
	}
	if d.u8() == 1 {
		value, found = d.bytes(), true
	}
	if err := d.finish(); err != nil {
		return nil, false, err
	}
	return value, found, nil
}

// Item is one key and value of a scan.
type Item struct {
	Key   string
	Value []byte
}

// ScanRequest asks for up to Limit keys of DB greater than After (exclusive;
// empty starts at the beginning), as of commit sequence At (0 reads the
// latest). Limit 0 means 100.
type ScanRequest struct {
	DB    string
	After string
	Limit int
	At    uint64
}

// Page is one page of a scan. At is the commit sequence it was read at: pass
// it as ScanRequest.At with After = Next for the following page. More is false
// on the last page.
type Page struct {
	At    uint64
	Items []Item
	More  bool
	Next  string
}

// Scan reads one page.
func (c *Client) Scan(ctx context.Context, r ScanRequest) (Page, error) {
	if r.DB == "" {
		return Page{}, &NotSentError{Err: ErrEmptyName}
	}
	if r.Limit <= 0 {
		r.Limit = 100
	}
	e := enc{b: []byte{opScan}}
	e.str(r.DB)
	e.str(r.After)
	e.u32(uint32(r.Limit))
	e.u64(r.At)
	d, err := c.read(ctx, e.b)
	if err != nil {
		return Page{}, err
	}
	var p Page
	p.At = d.u64()
	n := d.count()
	p.Items = make([]Item, 0, n)
	for i := 0; i < n && d.err == nil; i++ {
		p.Items = append(p.Items, Item{Key: d.str(), Value: d.bytes()})
	}
	p.More = d.u8() == 1
	p.Next = d.str()
	if err := d.finish(); err != nil {
		return Page{}, err
	}
	if p.More && (p.Next == "" || p.Next <= r.After) {
		return Page{}, fmt.Errorf("%w: next_after %q does not advance past %q", ErrProtocol, p.Next, r.After)
	}
	return p, nil
}

// ScanAll calls fn for every key of db in order, page by page of limit keys,
// all pages at the commit sequence of the first. When the server no longer has
// that commit (NoSuchCommit) the page is read again at the latest commit and
// the following pages pin to it, so a scan that outlives its snapshot goes on
// after the last key it delivered, no longer from one snapshot. It stops at
// the first other error.
func (c *Client) ScanAll(ctx context.Context, db string, limit int, fn func(Item) error) error {
	req := ScanRequest{DB: db, Limit: limit}
	for {
		p, err := c.Scan(ctx, req)
		if err != nil && req.At != 0 && pinGone(err) {
			// The pinned commit is gone: read this page at the latest commit (At 0) and pin to that.
			req.At = 0
			continue
		}
		if err != nil {
			return err
		}
		for _, it := range p.Items {
			if err := fn(it); err != nil {
				return err
			}
		}
		if !p.More {
			return nil
		}
		req.After, req.At = p.Next, p.At
	}
}

// pinGone reports whether err says the commit a pinned scan read at no longer
// exists (NoSuchCommit).
func pinGone(err error) bool {
	var rj *RejectedError
	return errors.As(err, &rj) && rj.Code == NoSuchCommit
}

// Health returns nil when mxdbd answers that it is serving.
func (c *Client) Health(ctx context.Context) error {
	d, err := c.read(ctx, []byte{opHealth})
	if err != nil {
		return err
	}
	return d.finish()
}

// read sends a read-only request and returns the decoder of an Ok reply.
func (c *Client) read(ctx context.Context, body []byte) (*dec, error) {
	reply, _, err := c.do(ctx, body, true)
	if err != nil {
		return nil, fmt.Errorf("mxdb: %w", err)
	}
	d := newDec(reply)
	st := d.u8()
	if st == stOK {
		return d, nil
	}
	_, err = decodeFailure(st, d)
	return nil, err
}

func encodeUpdate(u Update) ([]byte, error) {
	if len(u.Ops) == 0 {
		return nil, ErrNoOps
	}
	var dbs []string
	idx := map[string]uint32{}
	ref := func(db string) (uint32, error) {
		if db == "" {
			return 0, ErrEmptyName
		}
		i, ok := idx[db]
		if !ok {
			i = uint32(len(dbs))
			idx[db] = i
			dbs = append(dbs, db)
		}
		return i, nil
	}
	var rest enc
	rest.u32(uint32(len(u.Pre)))
	for _, p := range u.Pre {
		i, err := ref(p.db)
		if err != nil || p.key == "" {
			return nil, ErrEmptyName
		}
		if p.absent {
			rest.u8(condAbsent)
		} else {
			rest.u8(condHolds)
		}
		rest.u32(i)
		rest.str(p.key)
		if !p.absent {
			rest.bytes(p.value)
		}
	}
	rest.u32(uint32(len(u.Ops)))
	for _, o := range u.Ops {
		i, err := ref(o.db)
		if err != nil || o.key == "" {
			return nil, ErrEmptyName
		}
		rest.u8(o.kind)
		rest.u32(i)
		rest.str(o.key)
		switch o.kind {
		case kindPut:
			rest.bytes(o.value)
		case kindDeleteRange:
			rest.str(o.end)
		case kindDelete:
		default:
			return nil, fmt.Errorf("mxdb: operation kind %d is not valid", o.kind)
		}
	}
	e := enc{b: []byte{opUpdate}}
	e.u32(uint32(len(dbs)))
	for _, db := range dbs {
		e.str(db)
	}
	e.b = append(e.b, rest.b...)
	if len(e.b) > MaxFrame {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(e.b))
	}
	return e.b, nil
}

// decodeUpdateReply maps a reply to its outcome. A malformed reply returns
// OutcomeNone and an ErrProtocol error; the caller treats that as unknown.
func decodeUpdateReply(reply []byte) (Result, error) {
	d := newDec(reply)
	st := d.u8()
	if st != stOK {
		return decodeFailure(st, d)
	}
	seq := d.u64()
	if err := d.finish(); err != nil {
		return Result{}, err
	}
	return Result{Outcome: OutcomeOk, CommitSeq: seq}, nil
}

// decodeFailure maps a non-Ok reply status and its fields to a Result and the
// error of that outcome.
func decodeFailure(st byte, d *dec) (Result, error) {
	switch st {
	case stAborted:
		i := d.u32()
		if err := d.finish(); err != nil {
			return Result{}, err
		}
		return Result{Outcome: OutcomeAborted, FailedPre: int(i)}, &AbortedError{FailedPre: int(i)}
	case stRejected:
		code, reason := RejectCode(d.u8()), d.str()
		if err := d.finish(); err != nil {
			return Result{}, err
		}
		return Result{Outcome: OutcomeRejected, Code: code, Reason: reason}, &RejectedError{Code: code, Reason: reason}
	case stProtocolError:
		reason := d.str()
		if err := d.finish(); err != nil {
			return Result{}, err
		}
		return Result{Outcome: OutcomeRejected, Reason: reason}, &ProtocolError{Reason: reason}
	case stUnknown:
		reason := d.str()
		if err := d.finish(); err != nil {
			return Result{}, err
		}
		return Result{Outcome: OutcomeUnknown, Reason: reason}, unknownErr(fmt.Errorf("mxdbd reported it: %s", reason))
	}
	return Result{}, fmt.Errorf("%w: reply status %d", ErrProtocol, st)
}

// do sends body and returns the reply body. sent reports whether any byte of
// the request may have reached the server; when it is false the request was
// not executed. A pooled connection that fails before any reply byte arrives
// (reset, closed by the server, nothing written) is retried once on a fresh
// connection, for updates too: the server may have closed it while idle. A
// timeout or a cancelled context is not retried, nor is a failure after the
// first reply byte, except for read-only requests, which are repeatable.
func (c *Client) do(ctx context.Context, body []byte, readOnly bool) (reply []byte, sent bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	for attempt := 0; ; attempt++ {
		cn, pooled, err := c.acquire(ctx, attempt > 0)
		if err != nil {
			return nil, false, err
		}
		reply, wrote, silent, reuse, err := c.exchange(ctx, cn, body)
		if err == nil && reuse {
			c.release(cn)
		} else {
			_ = cn.Close()
		}
		if err == nil {
			return reply, true, nil
		}
		if pooled && attempt == 0 && ctx.Err() == nil && (silent || readOnly) {
			continue
		}
		return nil, wrote, err
	}
}

func (c *Client) acquire(ctx context.Context, fresh bool) (cn net.Conn, pooled bool, err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, false, ErrClosed
	}
	if n := len(c.idle); n > 0 && !fresh {
		cn = c.idle[n-1]
		c.idle = c.idle[:n-1]
		c.mu.Unlock()
		return cn, true, nil
	}
	c.mu.Unlock()
	dctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	cn, err = c.cfg.Dial(dctx)
	if err != nil {
		return nil, false, err
	}
	e := enc{b: []byte{opHello}}
	e.u32(Version)
	reply, _, _, _, err := c.exchange(dctx, cn, e.b)
	if err == nil {
		d := newDec(reply)
		if st := d.u8(); st != stOK {
			_, err = decodeFailure(st, d)
		} else {
			v := d.u32()
			if err = d.finish(); err == nil && v != Version {
				err = fmt.Errorf("%w: server speaks version %d, client %d", ErrProtocol, v, Version)
			}
		}
	}
	if err != nil {
		_ = cn.Close()
		return nil, false, fmt.Errorf("mxdb: hello: %w", err)
	}
	return cn, false, nil
}

func (c *Client) release(cn net.Conn) {
	c.mu.Lock()
	if !c.closed && len(c.idle) < c.cfg.MaxIdle {
		c.idle = append(c.idle, cn)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	_ = cn.Close()
}

// countReader counts the bytes read through it.
type countReader struct {
	r io.Reader
	n int
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// exchange writes one request frame on cn and reads one reply frame. wrote
// reports whether any byte of the request was accepted by the connection;
// silent, on an error, that no reply byte arrived and the failure was not a
// timeout or a cancelled context; reuse that cn is still fit for another call
// (a ProtocolError reply makes it unfit: the server closes that connection).
func (c *Client) exchange(ctx context.Context, cn net.Conn, body []byte) (reply []byte, wrote, silent, reuse bool, err error) {
	dl := time.Now().Add(c.cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	if err := cn.SetDeadline(dl); err != nil {
		return nil, false, false, false, err
	}
	stop := context.AfterFunc(ctx, func() { _ = cn.SetDeadline(time.Unix(1, 0)) })
	n, err := cn.Write(appendFrame(body))
	cr := &countReader{r: cn}
	if err == nil {
		reply, err = readFrame(cr)
	}
	reuse = stop() && err == nil && reply[0] != stProtocolError
	if err != nil {
		silent = cr.n == 0 && ctx.Err() == nil && !errors.Is(err, os.ErrDeadlineExceeded)
		cerr := ctx.Err()
		if d, ok := ctx.Deadline(); cerr == nil && ok && errors.Is(err, os.ErrDeadlineExceeded) && !time.Now().Before(d) {
			cerr = context.DeadlineExceeded // the connection's deadline is the context's and fired first
		}
		if cerr != nil {
			err = fmt.Errorf("%w: %v", cerr, err)
		}
		return nil, n > 0, silent, false, err
	}
	return reply, true, false, reuse, nil
}
