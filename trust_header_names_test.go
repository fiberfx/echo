// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"testing"

	"github.com/fiberfx/mx-echo/v7/internal/trust"
)

// TestTrustHeaderNames pins each forwarding header of internal/trust to the root's constant for the same header, so
// that an edit to one side is caught here and not only through the ServeHTTP golden test.
func TestTrustHeaderNames(t *testing.T) {
	if err := checkHeaderPairs(trustHeaderPairs()); err != nil {
		t.Error(err)
	}
}

func TestTrustHeaderNamesViolation(t *testing.T) {
	pairs := trustHeaderPairs()
	pairs[0][0] = "X-Forwarded-for"
	if checkHeaderPairs(pairs) == nil {
		t.Error("a trust header spelled unlike the root's passed the check")
	}
}

// trustHeaderPairs returns each header as internal/trust spells it, then as the root does.
func trustHeaderPairs() [][2]string {
	return [][2]string{
		{trust.HeaderXForwardedFor, HeaderXForwardedFor},
		{trust.HeaderXRealIP, HeaderXRealIP},
		{trust.HeaderXForwardedProto, HeaderXForwardedProto},
		{trust.HeaderXForwardedProtocol, HeaderXForwardedProtocol},
		{trust.HeaderXForwardedSsl, HeaderXForwardedSsl},
		{trust.HeaderXUrlScheme, HeaderXUrlScheme},
	}
}

// checkHeaderPairs returns an error for the first pair whose two spellings differ.
func checkHeaderPairs(pairs [][2]string) error {
	for _, p := range pairs {
		if p[0] != p[1] {
			return &headerPairError{trustName: p[0], rootName: p[1]}
		}
	}
	return nil
}

type headerPairError struct{ trustName, rootName string }

func (e *headerPairError) Error() string {
	return "internal/trust spells " + e.trustName + " and the root " + e.rootName
}
