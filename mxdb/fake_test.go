package mxdb

import (
	"bytes"
	"net"
	"sort"
	"sync"
	"testing"
)

type state map[string]map[string][]byte

func (s state) clone() state {
	out := state{}
	for db, kv := range s {
		m := make(map[string][]byte, len(kv))
		for k, v := range kv {
			m[k] = v
		}
		out[db] = m
	}
	return out
}

// fakeServer is an in-process mxdbd that speaks the frames of doc.go over a
// loopback listener. It keeps every committed state, so reads at a commit
// sequence work. The knobs make it fail the way the real one can.
type fakeServer struct {
	ln   net.Listener
	done chan struct{}
	wg   sync.WaitGroup

	mu         sync.Mutex
	snaps      []state // snaps[seq] is the state after commit seq
	conns      int
	live       []net.Conn
	hellos     []uint32
	updates    [][]byte
	scans      int
	dropBefore int           // drop the connection on the next n updates, before applying
	dropAfter  int           // drop the connection on the next n updates, after applying
	force      []byte        // reply body for the next update instead of executing it
	stall      chan struct{} // updates wait for this to close
	forget     uint64        // pinned reads at a commit below this fail with NoSuchCommit
	scanFail   bool          // pinned scans fail with Io
	truncate   bool          // the next reply is cut after two bytes and the connection dropped
}

func newFake(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{ln: ln, done: make(chan struct{}), snaps: []state{{}}}
	f.wg.Add(1)
	go f.accept()
	t.Cleanup(f.close)
	return f
}

// with runs fn holding the server's lock, for the test's knobs.
func (f *fakeServer) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeServer) close() {
	close(f.done)
	_ = f.ln.Close()
	f.dropConns()
	f.wg.Wait()
}

func (f *fakeServer) dropConns() {
	f.mu.Lock()
	live := f.live
	f.live = nil
	f.mu.Unlock()
	for _, cn := range live {
		_ = cn.Close()
	}
}

func (f *fakeServer) accept() {
	defer f.wg.Done()
	for {
		cn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns++
		f.live = append(f.live, cn)
		f.mu.Unlock()
		f.wg.Add(1)
		go f.serve(cn)
	}
}

func (f *fakeServer) serve(cn net.Conn) {
	defer f.wg.Done()
	defer cn.Close()
	for {
		body, err := readFrame(cn)
		if err != nil {
			return
		}
		reply, drop := f.handle(body)
		if drop {
			return
		}
		f.mu.Lock()
		cut := f.truncate
		f.truncate = false
		f.mu.Unlock()
		if cut {
			_, _ = cn.Write(appendFrame(reply)[:2])
			return
		}
		if _, err := cn.Write(appendFrame(reply)); err != nil {
			return
		}
	}
}

func mkReply(st byte, fill func(e *enc)) []byte {
	e := enc{b: []byte{st}}
	if fill != nil {
		fill(&e)
	}
	return e.b
}

func (f *fakeServer) handle(body []byte) ([]byte, bool) {
	d := newDec(body)
	switch op := d.u8(); op {
	case opHello:
		v := d.u32()
		f.mu.Lock()
		f.hellos = append(f.hellos, v)
		f.mu.Unlock()
		return mkReply(stOK, func(e *enc) { e.u32(Version) }), false
	case opUpdate:
		return f.update(body, d)
	case opSnapshot:
		f.mu.Lock()
		defer f.mu.Unlock()
		return mkReply(stOK, func(e *enc) { e.u64(uint64(len(f.snaps) - 1)) }), false
	case opGet:
		return f.get(d), false
	case opScan:
		return f.scan(d), false
	case opHealth:
		return mkReply(stOK, nil), false
	default:
		return protoErr("unknown operation"), false
	}
}

func rejected(code RejectCode, why string) []byte {
	return mkReply(stRejected, func(e *enc) { e.u8(byte(code)); e.str(why) })
}

func protoErr(why string) []byte {
	return mkReply(stProtocolError, func(e *enc) { e.str(why) })
}

func (f *fakeServer) at(at uint64) (state, uint64, bool) {
	if at != 0 && at < f.forget {
		return nil, 0, false
	}
	if at == 0 {
		at = uint64(len(f.snaps) - 1)
	}
	if at >= uint64(len(f.snaps)) {
		return nil, 0, false
	}
	return f.snaps[at], at, true
}

func (f *fakeServer) get(d *dec) []byte {
	db, key, at := d.str(), d.str(), d.u64()
	if d.finish() != nil {
		return protoErr("malformed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, _, ok := f.at(at)
	if !ok {
		return rejected(NoSuchCommit, "unknown snapshot")
	}
	v, found := s[db][key]
	return mkReply(stOK, func(e *enc) {
		if !found {
			e.u8(0)
			return
		}
		e.u8(1)
		e.bytes(v)
	})
}

func (f *fakeServer) scan(d *dec) []byte {
	db, after, limit, at := d.str(), d.str(), int(d.u32()), d.u64()
	if d.finish() != nil {
		return protoErr("malformed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans++
	if f.scanFail && at != 0 {
		return rejected(Io, "disk error")
	}
	s, at, ok := f.at(at)
	if !ok {
		return rejected(NoSuchCommit, "unknown snapshot")
	}
	var keys []string
	for k := range s[db] {
		if k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	return mkReply(stOK, func(e *enc) {
		e.u64(at)
		e.u32(uint32(len(keys)))
		for _, k := range keys {
			e.str(k)
			e.bytes(s[db][k])
		}
		if more {
			e.u8(1)
			e.str(keys[len(keys)-1])
			return
		}
		e.u8(0)
		e.str("")
	})
}

type fakeCond struct {
	kind byte
	db   uint32
	key  string
	val  []byte
}

type fakeOp struct {
	kind byte
	db   uint32
	key  string
	val  []byte
	end  string
}

func (f *fakeServer) update(body []byte, d *dec) ([]byte, bool) {
	f.mu.Lock()
	f.updates = append(f.updates, append([]byte{}, body...))
	if f.dropBefore > 0 {
		f.dropBefore--
		f.mu.Unlock()
		return nil, true
	}
	if f.force != nil {
		r := f.force
		f.force = nil
		f.mu.Unlock()
		return r, false
	}
	stall := f.stall
	f.mu.Unlock()
	if stall != nil {
		select {
		case <-stall:
		case <-f.done:
			return nil, true
		}
	}

	dbs := make([]string, d.count())
	for i := range dbs {
		dbs[i] = d.str()
	}
	pres := make([]fakeCond, d.count())
	for i := range pres {
		p := fakeCond{kind: d.u8(), db: d.u32(), key: d.str()}
		if p.kind == condHolds {
			p.val = d.bytes()
		}
		pres[i] = p
	}
	ops := make([]fakeOp, d.count())
	for i := range ops {
		o := fakeOp{kind: d.u8(), db: d.u32(), key: d.str()}
		switch o.kind {
		case kindPut:
			o.val = d.bytes()
		case kindDeleteRange:
			o.end = d.str()
		}
		ops[i] = o
	}
	if d.finish() != nil {
		return protoErr("malformed update"), false
	}
	for _, o := range ops {
		if len(o.key) > 64 {
			return rejected(KeyTooLong, "key is longer than 64 bytes"), false
		}
	}
	for _, db := range dbs {
		if db == "readonly" {
			return rejected(Other, "database readonly is read-only"), false
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.snaps[len(f.snaps)-1]
	for i, p := range pres {
		if int(p.db) >= len(dbs) {
			return protoErr("database index out of range"), false
		}
		v, ok := cur[dbs[p.db]][p.key]
		if (p.kind == condHolds && (!ok || !bytes.Equal(v, p.val))) || (p.kind == condAbsent && ok) {
			return mkReply(stAborted, func(e *enc) { e.u32(uint32(i)) }), false
		}
	}
	next := cur.clone()
	for _, o := range ops {
		if int(o.db) >= len(dbs) {
			return protoErr("database index out of range"), false
		}
		name := dbs[o.db]
		if next[name] == nil {
			next[name] = map[string][]byte{}
		}
		switch o.kind {
		case kindPut:
			next[name][o.key] = o.val
		case kindDelete:
			delete(next[name], o.key)
		case kindDeleteRange:
			for k := range next[name] {
				if k >= o.key && k < o.end {
					delete(next[name], k)
				}
			}
		}
	}
	f.snaps = append(f.snaps, next)
	seq := uint64(len(f.snaps) - 1)
	if f.dropAfter > 0 {
		f.dropAfter--
		return nil, true
	}
	return mkReply(stOK, func(e *enc) { e.u64(seq) }), false
}
