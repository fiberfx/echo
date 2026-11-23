// SPDX-License-Identifier: MIT

package ownerstore

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
)

// scanWAL reads the write-ahead log at path without opening the database and returns its length and the length of
// its torn tail: the bytes after the last valid commit frame that belong to the log's current generation (their
// salts equal the header's) and so are an unacknowledged transaction, a torn frame or a frame that fails its
// checksum. SQLite discards them on recovery. Bytes of an older generation, left behind after the log was reset,
// are not counted. A missing, empty or unreadable log gives zero.
func scanWAL(path string) (size, torn int64) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, 0
	}
	size = st.Size()
	var hdr [32]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return size, 0
	}
	be := binary.BigEndian
	magic := be.Uint32(hdr[0:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		return size, 0
	}
	bigEndian := magic&1 == 1
	pageSize := int64(be.Uint32(hdr[8:12]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 {
		return size, 0
	}
	s0, s1 := walSum(bigEndian, hdr[:24], 0, 0)
	if s0 != be.Uint32(hdr[24:28]) || s1 != be.Uint32(hdr[28:32]) {
		return size, 0
	}
	salt := hdr[16:24]
	frameLen := 24 + pageSize
	buf := make([]byte, frameLen)
	pos, committedEnd := int64(32), int64(32)
	valid := true
	for {
		n, err := io.ReadFull(f, buf)
		if n < 16 || !bytes.Equal(buf[8:16], salt) {
			break
		}
		if valid && int64(n) == frameLen {
			t0, t1 := walSum(bigEndian, buf[:8], s0, s1)
			t0, t1 = walSum(bigEndian, buf[24:], t0, t1)
			if t0 == be.Uint32(buf[16:20]) && t1 == be.Uint32(buf[20:24]) {
				s0, s1 = t0, t1
				if be.Uint32(buf[4:8]) != 0 {
					committedEnd = pos + frameLen
				}
			} else {
				valid = false
			}
		} else {
			valid = false
		}
		pos += int64(n)
		if err != nil {
			break
		}
	}
	return size, pos - committedEnd
}

// walSum is the checksum of the WAL format: two running sums over 8-byte units.
func walSum(bigEndian bool, b []byte, s0, s1 uint32) (uint32, uint32) {
	order := binary.ByteOrder(binary.LittleEndian)
	if bigEndian {
		order = binary.BigEndian
	}
	for i := 0; i+8 <= len(b); i += 8 {
		s0 += order.Uint32(b[i:]) + s1
		s1 += order.Uint32(b[i+4:]) + s0
	}
	return s0, s1
}
