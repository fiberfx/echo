// SPDX-License-Identifier: MIT

package fibervisor

import "github.com/fiberfx/mx-echo/v7/reqid"

// RequestNode returns the node id fv would derive for the request named id under this attempt, the path
// s.Path+"/q"+id: derived, not recorded. fv holds no record for it, so fv show refuses it. It is false for the zero
// Supervision or the zero ID, and for a Supervision whose Trace or AttemptNode is not the shape Read gives.
func (s Supervision) RequestNode(id reqid.ID) (string, bool) {
	if !s.held() || id.IsZero() {
		return "", false
	}
	return nodeID(s.Trace, s.Path+"/q"+id.String()), true
}

// held reports whether s has the shape Read gives an attempt: a Trace of 32 lowercase hex digits, an AttemptNode
// of 16 and a Path. The zero Supervision is not held.
func (s Supervision) held() bool {
	return isHex(s.Trace, traceEnd-traceStart) && isHex(s.AttemptNode, spanEnd-spanStart) && s.Path != ""
}

// traceparent returns the version 00 traceparent fv writes for node in s's trace: sampled.
func (s Supervision) traceparent(node string) string {
	return "00-" + s.Trace + "-" + node + "-01"
}
