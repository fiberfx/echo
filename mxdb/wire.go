package mxdb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Version is the protocol version sent in hello.
const Version = 1

// MaxFrame is the largest frame body in either direction, in bytes.
const MaxFrame = 1 << 20

// Request operations.
const (
	opHello byte = iota + 1
	opUpdate
	opSnapshot
	opGet
	opScan
	opHealth
)

// Reply statuses; the first four are the outcomes of an update.
const (
	stOK byte = iota
	stAborted
	stRejected
	stUnknown
)

// Precondition and operation kinds inside an update.
const (
	condHolds byte = iota + 1
	condAbsent
)

const (
	kindPut byte = iota + 1
	kindDelete
	kindDeleteRange
)

type enc struct{ b []byte }

func (e *enc) u8(v byte)    { e.b = append(e.b, v) }
func (e *enc) u32(v uint32) { e.b = binary.BigEndian.AppendUint32(e.b, v) }
func (e *enc) u64(v uint64) { e.b = binary.BigEndian.AppendUint64(e.b, v) }
func (e *enc) bytes(v []byte) {
	e.u32(uint32(len(v)))
	e.b = append(e.b, v...)
}
func (e *enc) str(v string) {
	e.u32(uint32(len(v)))
	e.b = append(e.b, v...)
}

var errShort = errors.New("short field")

type dec struct {
	b   []byte
	err error
}

func newDec(b []byte) *dec { return &dec{b: b} }

func (d *dec) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.b) {
		d.err = errShort
		return nil
	}
	v := d.b[:n]
	d.b = d.b[n:]
	return v
}

func (d *dec) u8() byte {
	v := d.take(1)
	if v == nil {
		return 0
	}
	return v[0]
}

func (d *dec) u32() uint32 {
	v := d.take(4)
	if v == nil {
		return 0
	}
	return binary.BigEndian.Uint32(v)
}

func (d *dec) u64() uint64 {
	v := d.take(8)
	if v == nil {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}

func (d *dec) bytes() []byte {
	n := d.u32()
	v := d.take(int(n))
	if v == nil {
		return nil
	}
	return append([]byte{}, v...)
}

func (d *dec) str() string {
	n := d.u32()
	return string(d.take(int(n)))
}

// count reads a list length and refuses one that cannot fit in what is left
// (every element is at least one byte).
func (d *dec) count() int {
	n := d.u32()
	if d.err == nil && int64(n) > int64(len(d.b)) {
		d.err = errShort
		return 0
	}
	return int(n)
}

// finish reports a malformed body: a short field or trailing bytes.
func (d *dec) finish() error {
	if d.err != nil {
		return fmt.Errorf("%w: %v", ErrProtocol, d.err)
	}
	if len(d.b) != 0 {
		return fmt.Errorf("%w: %d trailing bytes", ErrProtocol, len(d.b))
	}
	return nil
}

// appendFrame returns the frame (length header and body) for body.
func appendFrame(body []byte) []byte {
	out := make([]byte, 0, 4+len(body))
	out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

// readFrame reads one frame and returns its body.
func readFrame(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > MaxFrame {
		return nil, fmt.Errorf("%w: frame length %d outside 1..%d", ErrProtocol, n, MaxFrame)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return b, nil
}
