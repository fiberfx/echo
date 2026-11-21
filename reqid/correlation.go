// SPDX-License-Identifier: MIT

package reqid

// The names of the COR carriers, spelled as ECD's BCS and fv spell them.
const (
	CorrelationBaggageKey = "bcs.correlation.id" // the W3C Baggage member, canonical (ECD BCS)
	CorrelationHeader     = "X-Correlation-ID"   // the HTTP header (ECD BCS)
	CorrelationEnv        = "X_CORRELATION_ID"   // the process environment variable fv passes on
	CorrelationMeta       = "x-correlation-id"   // the key in an MCP message's params._meta
)

// correlationNS is the namespace of a COR id.
const correlationNS = "COR"

// Correlation is a COR id, the flow a request belongs to: a branded id of namespace COR, kept as its 14 bytes. It is
// comparable, and never a request's name. The zero Correlation means "none".
type Correlation struct{ b [14]byte }

// ParseCorrelation reads a COR id: a branded id that DecodeBranded accepts, of namespace COR. It reports false for an
// empty value, which is absent, and for a malformed one or one of another namespace.
func ParseCorrelation(s string) (Correlation, bool) {
	if ns, _, class := DecodeBranded(s); class != "" || ns != correlationNS {
		return Correlation{}, false
	}
	var c Correlation
	copy(c.b[:], s)
	return c, true
}

// NewCorrelation returns the COR id of a snowflake. It reports false when the snowflake is 2^63 or more.
func NewCorrelation(snowflake uint64) (Correlation, bool) {
	var c Correlation
	if !encodeBranded(&c.b, correlationNS, snowflake) {
		return Correlation{}, false
	}
	return c, true
}

// CorrelationOf is the one rule that reads a request's COR from HTTP, given the request's Baggage header values in
// order and its X-Correlation-ID header: the first Baggage member whose key is CorrelationBaggageKey gives the COR
// when its value, without properties and trimmed of spaces and tabs, is a valid COR; else the header gives it when it
// is one; else the zero Correlation. A caller passes r.Header.Values("Baggage") and r.Header.Get(CorrelationHeader).
// It allocates nothing.
func CorrelationOf(baggage []string, header string) Correlation {
	if c, ok := ParseCorrelation(baggageValue(baggage, CorrelationBaggageKey)); ok {
		return c
	}
	c, _ := ParseCorrelation(header)
	return c
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

// String returns the 14 bytes of the id, or "" for the zero Correlation.
func (c Correlation) String() string {
	if c.IsZero() {
		return ""
	}
	return string(c.b[:])
}

// AppendText appends String's text without allocating; the error is always nil.
func (c Correlation) AppendText(b []byte) ([]byte, error) {
	if c.IsZero() {
		return b, nil
	}
	return append(b, c.b[:]...), nil
}

// MarshalText returns String's text, so that encoding/json and log/slog's JSON handler write the id and not {};
// the error is always nil.
func (c Correlation) MarshalText() ([]byte, error) {
	return c.AppendText(make([]byte, 0, len(c.b)))
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
