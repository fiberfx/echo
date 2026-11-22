// SPDX-License-Identifier: MIT

package reqid

// The Ecd ID (eid), version 1: 16 bytes of system (15 bits), kind (15), reserved (2), Unix milliseconds (48), writer
// (30) and count (18), written as 28 lowercase characters such as emq_job_23obzcbapvupu2272222. The layout, the
// alphabet and the strict parse are those of spec/id.md of the ecd-id repository; the vectors in
// testdata/eid are its conformance contract.

// Alphabet holds the 32 symbols of an eid's text in value order; ASCII order equals value order.
const Alphabet = "234567abcdefghijklmnopqrstuvwxyz"

// The layout limits.
const (
	TextLen   = 28        // the length of an eid's text
	TimeMax   = 1<<48 - 1 // the largest millisecond an eid holds
	WriterMax = 1<<30 - 1 // the largest writer number
	countMax  = 1 << 18   // a writer's ids inside one millisecond
)

// Refusal is an error whose text is the reason that the specification and the vectors name.
type Refusal string

func (r Refusal) Error() string { return "reqid: " + string(r) }

// Reason returns the name of the refusal, as the vectors spell it.
func (r Refusal) Reason() string { return string(r) }

// The refusals of the parse and of the minter.
const (
	ErrLength         Refusal = "length"          // not 28 bytes (text) or 16 bytes (binary)
	ErrSeparator      Refusal = "separator"       // byte 3 or 7 is not an underscore
	ErrAlphabet       Refusal = "alphabet"        // a byte outside the alphabet, in any case but lower
	ErrPad            Refusal = "pad"             // the two pad bits are not zero
	ErrReserved       Refusal = "reserved"        // the reserved bits are not zero
	ErrSystemReserved Refusal = "system-reserved" // the system's first character is 2
	ErrKind           Refusal = "kind"            // the kind is not three letters
	ErrRange          Refusal = "range"           // a field is outside its width
	ErrLeaseSyntax    Refusal = "lease-syntax"    // the lease text is not system:writer:not_before:not_after
	ErrLeaseSystem    Refusal = "lease-system"    // the lease's system is reserved, or is another system's
	ErrLeaseWriter    Refusal = "lease-writer"    // the writer is 2^30 or more
	ErrLeaseWindow    Refusal = "lease-window"    // the window is empty, starts at zero or is longer than 24 hours
	ErrLeaseEnded     Refusal = "lease-ended"     // the minter has no id-time left inside its lease
	ErrFork           Refusal = "fork"            // the minter was made under another process id
	ErrRandom         Refusal = "random"          // the random source failed
)

const notInAlphabet = 0xff

var alphabetValue = func() (t [256]byte) {
	for i := range t {
		t[i] = notInAlphabet
	}
	for i := 0; i < 32; i++ {
		t[Alphabet[i]] = byte(i)
	}
	return t
}()

// Code is a 15-bit code of three alphabet characters: a system code or a kind.
type Code uint16

// Class is the class of a system code, read from its first character.
type Class int

// The three classes.
const (
	Registered Class = iota // a letter: a row in the system registry
	Detached                // a digit 3 to 7: drawn at random by a producer that holds no lease
	Reserved                // the digit 2: reserved for a later format, and refused
)

func (c Class) String() string {
	return [...]string{"registered", "detached", "reserved"}[c]
}

// ParseCode parses three alphabet characters. Uppercase and every other byte are refused.
func ParseCode(s string) (Code, bool) {
	if len(s) != 3 {
		return 0, false
	}
	var v uint16
	for i := 0; i < 3; i++ {
		x := alphabetValue[s[i]]
		if x == notInAlphabet {
			return 0, false
		}
		v = v<<5 | uint16(x)
	}
	return Code(v), true
}

// Class returns the class of c read as a system code.
func (c Code) Class() Class {
	switch first := c >> 10; {
	case first == 0:
		return Reserved
	case first <= 5:
		return Detached
	default:
		return Registered
	}
}

// IsKind reports whether c is a valid kind: three letters.
func (c Code) IsKind() bool {
	return (c>>10)&31 >= 6 && (c>>5)&31 >= 6 && c&31 >= 6
}

func (c Code) String() string {
	return string([]byte{Alphabet[(c>>10)&31], Alphabet[(c>>5)&31], Alphabet[c&31]})
}

// EID is an Ecd ID: 16 bytes, big-endian, so that comparing the bytes compares system, kind, time, writer and count.
// The zero EID is not a valid id.
type EID [16]byte

func (id EID) halves() (hi, lo uint64) {
	for i := 0; i < 8; i++ {
		hi = hi<<8 | uint64(id[i])
		lo = lo<<8 | uint64(id[8+i])
	}
	return hi, lo
}

func fromHalves(hi, lo uint64) (id EID) {
	for i := 7; i >= 0; i-- {
		id[i] = byte(hi)
		id[8+i] = byte(lo)
		hi >>= 8
		lo >>= 8
	}
	return id
}

func packEID(system, kind Code, time uint64, writer, count uint32) EID {
	hi := uint64(system)<<49 | uint64(kind)<<34 | time>>16
	lo := (time&0xffff)<<48 | uint64(writer)<<18 | uint64(count)
	return fromHalves(hi, lo)
}

// EIDFromParts builds an id from its fields, checking each against the layout.
func EIDFromParts(system, kind Code, time uint64, writer, count uint32) (EID, error) {
	if system.Class() == Reserved {
		return EID{}, ErrSystemReserved
	}
	if !kind.IsKind() {
		return EID{}, ErrKind
	}
	if system >= 1<<15 || kind >= 1<<15 || time > TimeMax || writer > WriterMax || count >= countMax {
		return EID{}, ErrRange
	}
	return packEID(system, kind, time, writer, count), nil
}

// checkEID runs steps 5 to 7 of the strict parse, which need no registry.
func checkEID(id EID) (EID, error) {
	hi, _ := id.halves()
	if (hi>>32)&3 != 0 {
		return EID{}, ErrReserved
	}
	if id.System().Class() == Reserved {
		return EID{}, ErrSystemReserved
	}
	if !id.Kind().IsKind() {
		return EID{}, ErrKind
	}
	return id, nil
}

// EIDFromBytes is binary ingress: exactly 16 bytes, then steps 5 to 7 of the strict parse.
func EIDFromBytes(b []byte) (EID, error) {
	if len(b) != 16 {
		return EID{}, ErrLength
	}
	var id EID
	copy(id[:], b)
	return checkEID(id)
}

// ParseEID is the strict parse of the canonical text, in the order of spec/id.md: the length, the separators, the
// alphabet, the pad bits, the reserved bits, the system's class and the kind. Each step refuses with its own reason
// and none repairs: there is no case folding, no alias and no skipped space. It allocates nothing.
func ParseEID(s string) (EID, error) {
	if len(s) != TextLen {
		return EID{}, ErrLength
	}
	if s[3] != '_' || s[7] != '_' {
		return EID{}, ErrSeparator
	}
	var hi, lo uint64
	var last byte
	i := 0
	for pos := 0; pos < TextLen; pos++ {
		if pos == 3 || pos == 7 {
			continue
		}
		v := alphabetValue[s[pos]]
		if v == notInAlphabet {
			return EID{}, ErrAlphabet
		}
		if i < 25 {
			hi = hi<<5 | lo>>59
			lo = lo<<5 | uint64(v)
		} else {
			last = v
		}
		i++
	}
	if last&3 != 0 {
		return EID{}, ErrPad
	}
	hi = hi<<3 | lo>>61
	lo = lo<<3 | uint64(last>>2)
	return checkEID(fromHalves(hi, lo))
}

// low64 returns the low 64 bits of the 128-bit value shifted right by k.
func low64(hi, lo uint64, k uint) uint64 {
	switch {
	case k == 0:
		return lo
	case k >= 64:
		return hi >> (k - 64)
	default:
		return lo>>k | hi<<(64-k)
	}
}

// Encode returns the canonical text as 28 bytes.
func (id EID) Encode() [TextLen]byte {
	var out [TextLen]byte
	hi, lo := id.halves()
	out[3], out[7] = '_', '_'
	for i := 0; i < 26; i++ {
		var v uint64
		if i < 25 {
			v = low64(hi, lo, uint(123-5*i)) & 31
		} else {
			v = (lo & 7) << 2
		}
		pos := i
		if i >= 6 {
			pos = i + 2
		} else if i >= 3 {
			pos = i + 1
		}
		out[pos] = Alphabet[v]
	}
	return out
}

// String returns the canonical text.
func (id EID) String() string {
	t := id.Encode()
	return string(t[:])
}

// Bytes returns the 16 bytes.
func (id EID) Bytes() []byte {
	b := id
	return b[:]
}

// System returns the system that minted the id.
func (id EID) System() Code {
	hi, _ := id.halves()
	return Code(hi >> 49)
}

// Kind returns the kind.
func (id EID) Kind() Code {
	hi, _ := id.halves()
	return Code((hi >> 34) & 0x7fff)
}

// Time returns the Unix milliseconds the minter stamped.
func (id EID) Time() uint64 {
	hi, lo := id.halves()
	return (hi&0xffffffff)<<16 | lo>>48
}

// Writer returns the writer number.
func (id EID) Writer() uint32 {
	_, lo := id.halves()
	return uint32(lo>>18) & WriterMax
}

// Count returns the count inside the writer's millisecond.
func (id EID) Count() uint32 {
	_, lo := id.halves()
	return uint32(lo) & (countMax - 1)
}

// Class returns the class of the id's system.
func (id EID) Class() Class {
	return id.System().Class()
}

// Compare compares two ids in byte order: -1, 0 or 1.
func (id EID) Compare(o EID) int {
	for i := range id {
		if id[i] != o[i] {
			if id[i] < o[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
