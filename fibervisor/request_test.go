// SPDX-License-Identifier: MIT

package fibervisor_test

import (
	"fmt"
	"testing"

	"github.com/fiberfx/mx-echo/v6/fibervisor"
	"github.com/fiberfx/mx-echo/v6/reqid"
)

// fvRequestID is the request whose path under fvAttempt is fvAttemptRequest.
var fvRequestID = reqid.New(2, 7)

// checkRequestNode returns an error unless node and ok are what RequestNode owes for s and id: the id derived from
// s.Trace and s.Path+"/q"+id and true, or "" and false when s or id is zero.
func checkRequestNode(s fibervisor.Supervision, id reqid.ID, node string, ok bool) error {
	if s == (fibervisor.Supervision{}) || id.IsZero() {
		if node != "" || ok {
			return fmt.Errorf("RequestNode(%v) = %q, %v under %+v, want \"\", false", id, node, ok, s)
		}
		return nil
	}
	if want := derive(s.Trace, s.Path+"/q"+id.String()); node != want || !ok {
		return fmt.Errorf("RequestNode(%v) = %q, %v under %+v, want %q, true", id, node, ok, s, want)
	}
	return nil
}

func TestRequestNode(t *testing.T) {
	if fvRequestID.String() != fvAttemptRequest[len(fvAttempt)+2:] {
		t.Fatalf("fvRequestID is %v, not the key of %s", fvRequestID, fvAttemptRequest)
	}
	leaf(fvTrace, fvAttempt).set(t)
	read, ok := fibervisor.Read()
	if !ok {
		t.Fatal("Read() is false under fv's leaf environment")
	}
	for _, tt := range []struct {
		name string
		s    fibervisor.Supervision
		id   reqid.ID
	}{
		{"fv's own vector's attempt", fvOwn, fvRequestID},
		{"the attempt Read gives", read, fvRequestID},
		{"a nested attempt", attemptOf(fvTrace, fvNested), reqid.New(1<<47-1, 1<<32-1)},
		{"the zero Supervision", fibervisor.Supervision{}, fvRequestID},
		{"the zero ID", fvOwn, reqid.ID{}},
	} {
		node, ok := tt.s.RequestNode(tt.id)
		if err := checkRequestNode(tt.s, tt.id, node, ok); err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
	// The vector's own request path, by the test's derive, which TestAttempt holds to fv's pins.
	if node, _ := fvOwn.RequestNode(fvRequestID); node != derive(fvOtherTrace, fvAttemptRequest) || node == fvAttemptNode {
		t.Errorf("RequestNode(%v) = %q, want %q, which is not the attempt's node", fvRequestID, node, derive(fvOtherTrace, fvAttemptRequest))
	}
}

// TestRequestNodeHandBuilt holds a Supervision that is not the shape Read gives to false, never a hex string from
// a trace or node fv did not write.
func TestRequestNodeHandBuilt(t *testing.T) {
	for _, s := range []fibervisor.Supervision{
		{Path: fvAttempt},
		{Trace: fvOtherTrace, Path: fvAttempt},
		{Trace: fvOtherTrace, AttemptNode: fvAttemptNode},
		{Trace: "not a trace", Path: fvAttempt, AttemptNode: fvAttemptNode},
		{Trace: fvOtherTrace, Path: fvAttempt, AttemptNode: "FD578C945E0AAC89"},
	} {
		if node, ok := s.RequestNode(fvRequestID); node != "" || ok {
			t.Errorf("RequestNode under %+v = %q, %v, want \"\", false", s, node, ok)
		}
	}
}

func TestRequestNodeViolation(t *testing.T) {
	cor, ok := reqid.NewCorrelation(1 << 40)
	if !ok {
		t.Fatal("NewCorrelation refused the test's snowflake")
	}
	for _, bad := range []struct {
		name string
		s    fibervisor.Supervision
		id   reqid.ID
		node string
		ok   bool
	}{
		{"derived from the path without /q", fvOwn, fvRequestID, derive(fvOtherTrace, fvAttempt+"/"+fvRequestID.String()), true},
		{"derived from the bare id", fvOwn, fvRequestID, derive(fvOtherTrace, fvAttempt+fvRequestID.String()), true},
		{"derived from the request's COR text", fvOwn, fvRequestID, derive(fvOtherTrace, fvAttempt+"/q"+cor.String()), true},
		{"the attempt's own node", fvOwn, fvRequestID, fvAttemptNode, true},
		{"a node from another trace", fvOwn, fvRequestID, derive(fvTrace, fvAttemptRequest), true},
		{"the right node with false", fvOwn, fvRequestID, derive(fvOtherTrace, fvAttemptRequest), false},
		{"a hex string for the zero ID", fvOwn, reqid.ID{}, derive(fvOtherTrace, fvAttempt+"/q"+reqid.ID{}.String()), true},
		{"a hex string for the zero Supervision", fibervisor.Supervision{}, fvRequestID, derive("", "/q"+fvRequestID.String()), true},
		{"a hex string with false for the zero ID", fvOwn, reqid.ID{}, fvAttemptNode, false},
	} {
		if checkRequestNode(bad.s, bad.id, bad.node, bad.ok) == nil {
			t.Errorf("%s passed the check", bad.name)
		}
	}
}
