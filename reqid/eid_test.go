// SPDX-License-Identifier: MIT

package reqid_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v6/reqid"
)

var (
	sinkEID reqid.EID
	sinkErr error
)

// vectorRows returns the data rows of a conformance file of the Ecd ID, copied from the ecd-id repository (see
// testdata/eid/SOURCE.txt): each row split at its tabs, the comment lines dropped.
func vectorRows(t testing.TB, name string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "eid", name))
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	if len(rows) == 0 {
		t.Fatalf("%s holds no row", name)
	}
	return rows
}

// vectorHex reads a hex column of the vectors; "-" is the empty input.
func vectorHex(s string) ([]byte, error) {
	if s == "-" {
		return []byte{}, nil
	}
	return hex.DecodeString(s)
}

// reason returns the name of the refusal err carries, "" for nil and the text of any other error.
func reason(err error) string {
	var r reqid.Refusal
	switch {
	case err == nil:
		return ""
	case errors.As(err, &r):
		return r.Reason()
	}
	return "not a Refusal: " + err.Error()
}

func parseUint(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		panic(err)
	}
	return v
}

// checkValid reads vectors/valid.tsv both ways: parse reads each text to its 16 bytes and fields, and the writer and
// the binary ingress give the text and the id back.
func checkValid(rows [][]string, parse func(string) (reqid.EID, error)) error {
	for _, r := range rows {
		text, wantHex := r[0], r[1]
		id, err := parse(text)
		if err != nil {
			return fmt.Errorf("%s: parse refused with %q, want an id", text, reason(err))
		}
		if got := hex.EncodeToString(id.Bytes()); got != wantHex {
			return fmt.Errorf("%s: parse gave bytes %s, want %s", text, got, wantHex)
		}
		if id.System().String() != r[2] || id.Kind().String() != r[3] || id.Time() != parseUint(r[4]) ||
			uint64(id.Writer()) != parseUint(r[5]) || uint64(id.Count()) != parseUint(r[6]) || id.Class().String() != r[7] {
			return fmt.Errorf("%s: fields %s %s %d %d %d %s, want %s", text, id.System(), id.Kind(), id.Time(), id.Writer(), id.Count(), id.Class(), strings.Join(r[2:8], " "))
		}
		if id.String() != text {
			return fmt.Errorf("%s: String() = %q", text, id.String())
		}
		if enc := id.Encode(); string(enc[:]) != text {
			return fmt.Errorf("%s: Encode() = %q", text, enc[:])
		}
		raw, _ := hex.DecodeString(wantHex)
		if back, err := reqid.EIDFromBytes(raw); err != nil || back != id {
			return fmt.Errorf("%s: EIDFromBytes = (%v, %v), want the parsed id", text, back, err)
		}
		system, _ := reqid.ParseCode(r[2])
		kind, _ := reqid.ParseCode(r[3])
		if built, err := reqid.EIDFromParts(system, kind, parseUint(r[4]), uint32(parseUint(r[5])), uint32(parseUint(r[6]))); err != nil || built != id {
			return fmt.Errorf("%s: EIDFromParts = (%v, %v), want the parsed id", text, built, err)
		}
	}
	return nil
}

// checkInvalid reads vectors/invalid.tsv: every text is refused, with the reason of the first step that fails.
func checkInvalid(rows [][]string, parse func(string) (reqid.EID, error)) error {
	for _, r := range rows {
		raw, err := vectorHex(r[1])
		if err != nil {
			return fmt.Errorf("%s: the vector's hex: %v", r[0], err)
		}
		id, err := parse(string(raw))
		if err == nil {
			return fmt.Errorf("%s: %q was accepted as %v, want the refusal %q", r[0], raw, id, r[2])
		}
		if got := reason(err); got != r[2] {
			return fmt.Errorf("%s: %q was refused with %q, want %q", r[0], raw, got, r[2])
		}
		if id != (reqid.EID{}) {
			return fmt.Errorf("%s: a refusal returned the id %v, want the zero EID", r[0], id)
		}
	}
	return nil
}

func TestEIDVectorsValid(t *testing.T) {
	rows := vectorRows(t, "valid.tsv")
	if len(rows) < 100 {
		t.Fatalf("valid.tsv has %d rows, want the whole file", len(rows))
	}
	if err := checkValid(rows, reqid.ParseEID); err != nil {
		t.Error(err)
	}
}

func TestEIDVectorsInvalid(t *testing.T) {
	rows := vectorRows(t, "invalid.tsv")
	if len(rows) < 25 {
		t.Fatalf("invalid.tsv has %d rows, want the whole file", len(rows))
	}
	if err := checkInvalid(rows, reqid.ParseEID); err != nil {
		t.Error(err)
	}
}

// TestEIDInvalidViolation plants a wrong pad check, a parse that clears the two pad bits instead of refusing them, and
// requires the table to catch it on the pad rows.
func TestEIDInvalidViolation(t *testing.T) {
	padless := func(s string) (reqid.EID, error) {
		id, err := reqid.ParseEID(s)
		if reason(err) != "pad" {
			return id, err
		}
		i := strings.IndexByte(reqid.Alphabet, s[len(s)-1])
		return reqid.ParseEID(s[:len(s)-1] + string(reqid.Alphabet[i&^3]))
	}
	err := checkInvalid(vectorRows(t, "invalid.tsv"), padless)
	if err == nil {
		t.Fatal("a parse with no pad check passed the invalid vectors")
	}
	if !strings.HasPrefix(err.Error(), "pad") {
		t.Errorf("a parse with no pad check was caught on another row: %v", err)
	}
}

func TestEIDBinaryIngress(t *testing.T) {
	for _, r := range vectorRows(t, "binary_invalid.tsv") {
		raw, err := vectorHex(r[1])
		if err != nil {
			t.Fatalf("%s: %v", r[0], err)
		}
		id, err := reqid.EIDFromBytes(raw)
		if err == nil || reason(err) != r[2] || id != (reqid.EID{}) {
			t.Errorf("%s: EIDFromBytes(%x) = (%v, %q), want the zero EID and %q", r[0], raw, id, reason(err), r[2])
		}
	}
	if _, err := reqid.EIDFromBytes(make([]byte, 16)); reason(err) != "system-reserved" {
		t.Errorf("sixteen zero bytes: %q, want system-reserved", reason(err))
	}
}

// Text order is byte order, over every pair of the valid vectors.
func TestEIDTextOrderIsByteOrder(t *testing.T) {
	rows := vectorRows(t, "valid.tsv")
	for _, a := range rows {
		for _, b := range rows {
			ba, _ := hex.DecodeString(a[1])
			bb, _ := hex.DecodeString(b[1])
			if got, want := strings.Compare(a[0], b[0]), bytes.Compare(ba, bb); got != want {
				t.Fatalf("%s against %s: text order %d, byte order %d", a[0], b[0], got, want)
			}
		}
	}
}

func TestEIDFromPartsRefusals(t *testing.T) {
	emq, _ := reqid.ParseCode("emq")
	job, _ := reqid.ParseCode("job")
	reserved, _ := reqid.ParseCode("2mq")
	digitKind, _ := reqid.ParseCode("j2b")
	for _, c := range []struct {
		name   string
		system reqid.Code
		kind   reqid.Code
		time   uint64
		writer uint32
		count  uint32
		want   string
	}{
		{"a reserved system", reserved, job, 1, 1, 0, "system-reserved"},
		{"a kind with a digit", emq, digitKind, 1, 1, 0, "kind"},
		{"a time of 49 bits", emq, job, 1 << 48, 1, 0, "range"},
		{"a writer of 31 bits", emq, job, 1, 1 << 30, 0, "range"},
		{"a count of 19 bits", emq, job, 1, 1, 1 << 18, "range"},
		{"the largest fields", emq, job, 1<<48 - 1, 1<<30 - 1, 1<<18 - 1, ""},
	} {
		id, err := reqid.EIDFromParts(c.system, c.kind, c.time, c.writer, c.count)
		if got := reason(err); got != c.want {
			t.Errorf("%s: EIDFromParts refused with %q, want %q", c.name, got, c.want)
		}
		if c.want == "" && (id.Time() != c.time || id.Writer() != c.writer || id.Count() != c.count) {
			t.Errorf("%s: the fields did not come back: %d %d %d", c.name, id.Time(), id.Writer(), id.Count())
		}
	}
}

func TestParseCode(t *testing.T) {
	for _, c := range []struct {
		in    string
		ok    bool
		class reqid.Class
		kind  bool
	}{
		{"emq", true, reqid.Registered, true},
		{"3ab", true, reqid.Detached, false},
		{"7zz", true, reqid.Detached, false},
		{"2ab", true, reqid.Reserved, false},
		{"j2b", true, reqid.Registered, false},
		{"EMQ", false, 0, false},
		{"em", false, 0, false},
		{"em1", false, 0, false},
		{"emqq", false, 0, false},
	} {
		code, ok := reqid.ParseCode(c.in)
		if ok != c.ok {
			t.Errorf("ParseCode(%q) ok = %t, want %t", c.in, ok, c.ok)
			continue
		}
		if ok && (code.String() != c.in || code.Class() != c.class || code.IsKind() != c.kind) {
			t.Errorf("ParseCode(%q) = %s, class %s, kind %t; want class %s, kind %t", c.in, code, code.Class(), code.IsKind(), c.class, c.kind)
		}
	}
}

func TestEIDAllocs(t *testing.T) {
	const text = "emq_job_23obzcbapvupu2272222"
	if err := checkNoAllocs(func() { sinkEID, sinkErr = reqid.ParseEID(text) }); err != nil {
		t.Errorf("ParseEID: %v", err)
	}
	if sinkErr != nil || sinkEID.String() != text {
		t.Errorf("ParseEID gave (%v, %v)", sinkEID, sinkErr)
	}
	if err := checkNoAllocs(func() { sinkEID, sinkErr = reqid.ParseEID(text[:27]) }); err != nil {
		t.Errorf("ParseEID of a refused text: %v", err)
	}
}
