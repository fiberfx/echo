// SPDX-License-Identifier: MIT

package reqid

// The names of the COR carriers, spelled as ECD's BCS and fv spell them.
const (
	CorrelationBaggageKey = "bcs.correlation.id" // the W3C Baggage member, canonical (ECD BCS); its value may be percent-encoded
	CorrelationHeader     = "X-Correlation-ID"   // the HTTP header (ECD BCS)
	CorrelationEnv        = "X_CORRELATION_ID"   // the process environment variable fv passes on
	CorrelationMeta       = "x-correlation-id"   // the key in an MCP message's params._meta
)

// corKind is the kind of a COR id.
var corKind, _ = ParseCode("cor")

// corSystems are the registered systems whose COR is accepted: those the ecd-id registry (registry/systems.tsv and
// registry/kinds.tsv at ecd-id 1fcc0f1, go/v0.6.1) holds in a state other than retired, with a cor row in a state other
// than retired. A test derives the set from the vendored copies and fails unless it equals this one.
var corSystems = func() []Code {
	mxe, _ := ParseCode("mxe")
	return []Code{mxe}
}()

// corSystemOK reports whether id's system may issue a COR: a detached-class system always, a registered one only when
// it is in corSystems.
func corSystemOK(id EID) bool {
	system := id.System()
	if system.Class() == Detached {
		return true
	}
	for _, c := range corSystems {
		if c == system {
			return true
		}
	}
	return false
}

// checkCorrelation is the one rule of a COR that every carrier applies: id is an eid of kind cor, and its system is
// detached-class or a registered system with a live cor row.
func checkCorrelation(id EID) bool {
	return id.Kind() == corKind && corSystemOK(id)
}

// Correlation is a COR id, the flow a request belongs to: an eid of kind cor from a detached-class system or from a
// registered system that has a live cor row in the ecd-id registry (today mxe alone; a COR of any other registered
// system, brd and ecd among them, is refused). It is comparable, and never a request's name. The zero Correlation
// means "none"; the zero EID is not a valid id, so no COR is equal to it.
type Correlation struct{ id EID }

// ParseCorrelation reads a COR id: an eid that ParseEID accepts, of kind cor. It reports false for an empty value,
// which is absent, for a malformed one, for one of another kind and for one of a system that may not issue a COR. It
// allocates nothing.
func ParseCorrelation(s string) (Correlation, bool) {
	id, err := ParseEID(s)
	if err != nil || !checkCorrelation(id) {
		return Correlation{}, false
	}
	return Correlation{id}, true
}

// NewCorrelation returns the COR of id. It reports false unless id is a valid eid of kind cor from an accepted system.
func NewCorrelation(id EID) (Correlation, bool) {
	if checked, err := checkEID(id); err != nil || !checkCorrelation(checked) {
		return Correlation{}, false
	}
	return Correlation{id}, true
}

// EID returns the COR's id; the zero EID for the zero Correlation.
func (c Correlation) EID() EID { return c.id }

// CorrelationSource names the carrier that gave a COR.
type CorrelationSource uint8

// The carriers a COR is read from; CorrelationFromNone is the source of the zero Correlation.
const (
	CorrelationFromNone CorrelationSource = iota
	CorrelationFromBaggage
	CorrelationFromHeader
)

func (s CorrelationSource) String() string {
	switch s {
	case CorrelationFromBaggage:
		return "baggage"
	case CorrelationFromHeader:
		return "header"
	}
	return "none"
}

// CorrelationReason is why a carrier gave, or did not give, a COR: ok, absent, escape, cor-kind, cor-system, or the
// name of the Refusal of the strict parse (length, separator, alphabet, pad, reserved, system-reserved, kind).
type CorrelationReason string

// The reasons that are not a Refusal of the strict parse.
const (
	ReasonOK        CorrelationReason = "ok"         // the carrier holds a valid COR
	ReasonAbsent    CorrelationReason = "absent"     // the carrier holds no value
	ReasonEscape    CorrelationReason = "escape"     // a percent escape of the Baggage value is malformed
	ReasonCorKind   CorrelationReason = "cor-kind"   // a valid eid of a kind other than cor
	ReasonCorSystem CorrelationReason = "cor-system" // a valid cor of a system that may not issue a COR
)

// CorrelationRead is what ReadCorrelation found: the COR, the carrier that gave it, and the reason for each carrier.
type CorrelationRead struct {
	Correlation Correlation       // the Baggage COR if Baggage is ok, else the header COR if Header is ok, else zero
	Source      CorrelationSource // the carrier of Correlation; CorrelationFromNone when it is zero
	Baggage     CorrelationReason // why the Baggage member gave, or did not give, a COR
	Header      CorrelationReason // why the header gave, or did not give, a COR
}

// ReadCorrelation is CorrelationOf with its reasons: it reads both carriers, the first Baggage member whose key is
// CorrelationBaggageKey (its value without properties, trimmed of spaces and tabs and percent-decoded as W3C Baggage
// allows) and the header, and reports for each why it did or did not give a COR. The Baggage COR wins over the
// header's. A decoded value that is not an eid text long is refused as length, and a malformed percent escape as
// escape. It allocates nothing.
func ReadCorrelation(baggage []string, header string) CorrelationRead {
	var r CorrelationRead
	var bag, hdr EID
	value := baggageValue(baggage, CorrelationBaggageKey)
	switch {
	case value == "":
		r.Baggage = ReasonAbsent
	case indexByte(value, '%') < 0:
		bag, r.Baggage = readCOR(value)
	default:
		var b [TextLen]byte
		if n, ok := percentDecode(&b, value); !ok {
			r.Baggage = ReasonEscape
		} else if n != len(b) {
			r.Baggage = CorrelationReason(ErrLength)
		} else {
			bag, r.Baggage = readCOR(string(b[:]))
		}
	}
	if header == "" {
		r.Header = ReasonAbsent
	} else {
		hdr, r.Header = readCOR(header)
	}
	switch {
	case r.Baggage == ReasonOK:
		r.Correlation, r.Source = Correlation{bag}, CorrelationFromBaggage
	case r.Header == ReasonOK:
		r.Correlation, r.Source = Correlation{hdr}, CorrelationFromHeader
	}
	return r
}

// readCOR is the strict parse of s, a non-empty value, with the COR rule: the id and the reason.
func readCOR(s string) (EID, CorrelationReason) {
	id, err := ParseEID(s)
	if err != nil {
		if refusal, ok := err.(Refusal); ok {
			return EID{}, CorrelationReason(refusal)
		}
		return EID{}, CorrelationReason(err.Error())
	}
	switch {
	case id.Kind() != corKind:
		return EID{}, ReasonCorKind
	case !corSystemOK(id):
		return EID{}, ReasonCorSystem
	}
	return id, ReasonOK
}

// CorrelationOf is the one rule that reads a request's COR from HTTP, given the request's Baggage header values in
// order and its X-Correlation-ID header: the first Baggage member whose key is CorrelationBaggageKey gives the COR
// when its value, without properties, trimmed of spaces and tabs and percent-decoded as W3C Baggage allows, is a valid
// COR; else the header gives it when it is one; else the zero Correlation. A caller passes r.Header.Values("Baggage")
// and r.Header.Get(CorrelationHeader). It is ReadCorrelation's Correlation, and allocates nothing.
func CorrelationOf(baggage []string, header string) Correlation {
	return ReadCorrelation(baggage, header).Correlation
}

// percentDecode decodes s, a Baggage value, into b, each %XX as the byte of its two hex digits, and returns the length
// of the decoded value, which is the number of bytes written to b only when it does not exceed len(b). It reports false
// for a malformed escape, wherever it stands, and true otherwise, so that a caller tells a malformed escape from a
// decoded value of the wrong length.
func percentDecode(b *[TextLen]byte, s string) (int, bool) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' {
			if i+2 >= len(s) {
				return 0, false
			}
			hi, okHi := hexValue(s[i+1])
			lo, okLo := hexValue(s[i+2])
			if !okHi || !okLo {
				return 0, false
			}
			c = hi<<4 | lo
			i += 2
		}
		if n < len(b) {
			b[n] = c
		}
		n++
	}
	return n, true
}

// hexValue returns the value of the hex digit c, either case.
func hexValue(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// baggageValue returns the value of the first member of the Baggage header values whose key is key, without its
// properties and trimmed of spaces and tabs, or "" when no member has that key.
func baggageValue(values []string, key string) string {
	for _, list := range values {
		for list != "" {
			member := list
			list = ""
			if i := indexByte(member, ','); i >= 0 {
				member, list = member[:i], member[i+1:]
			}
			i := indexByte(member, '=')
			if i < 0 || trimSpaceTab(member[:i]) != key {
				continue
			}
			value := member[i+1:]
			if j := indexByte(value, ';'); j >= 0 {
				value = value[:j]
			}
			return trimSpaceTab(value)
		}
	}
	return ""
}

// IsZero reports whether c is the zero Correlation.
func (c Correlation) IsZero() bool {
	return c == Correlation{}
}

// String returns the 28 characters of the id, or "" for the zero Correlation.
func (c Correlation) String() string {
	if c.IsZero() {
		return ""
	}
	return c.id.String()
}

// AppendText appends String's text without allocating; the error is always nil.
func (c Correlation) AppendText(b []byte) ([]byte, error) {
	if c.IsZero() {
		return b, nil
	}
	t := c.id.Encode()
	return append(b, t[:]...), nil
}

// MarshalText returns String's text, so that encoding/json and log/slog's JSON handler write the id and not {};
// the error is always nil.
func (c Correlation) MarshalText() ([]byte, error) {
	return c.AppendText(make([]byte, 0, TextLen))
}

// indexByte returns the index of the first c in s, or -1.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// trimSpaceTab returns s without its leading and trailing spaces and tabs.
func trimSpaceTab(s string) string {
	for s != "" && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for s != "" && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
