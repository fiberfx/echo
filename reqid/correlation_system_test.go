// SPDX-License-Identifier: MIT

package reqid_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/fiberfx/mx-echo/v7/reqid"
)

// corOf builds the COR text of system with reqid's own encoder.
func corOf(t testing.TB, system string) (reqid.EID, string) {
	t.Helper()
	sys, ok := reqid.ParseCode(system)
	cor, _ := reqid.ParseCode("cor")
	if !ok {
		t.Fatalf("%q is not a system code", system)
	}
	id, err := reqid.EIDFromParts(sys, cor, 1795271400123, 895287301, 0)
	if err != nil {
		t.Fatal(err)
	}
	return id, id.String()
}

// checkCorRefused holds that the cor of system is a valid eid of kind cor from a registered system, and that every
// carrier refuses it for that system alone.
func checkCorRefused(t *testing.T, system string) {
	t.Helper()
	id, text := corOf(t, system)
	if parsed, err := reqid.ParseEID(text); err != nil || parsed != id || id.Kind().String() != "cor" ||
		id.System().String() != system || id.Class() != reqid.Registered {
		t.Fatalf("%s: %q is not a valid registered-system cor (%v), so its refusal would have another reason", system, text, err)
	}
	if c, ok := reqid.ParseCorrelation(text); ok || !c.IsZero() {
		t.Errorf("ParseCorrelation(%q) = (%q, %t), want the zero Correlation and false: system %s has no live cor row", text, c, ok, system)
	}
	if c, ok := reqid.NewCorrelation(id); ok || !c.IsZero() {
		t.Errorf("NewCorrelation(%q) = (%q, %t), want the zero Correlation and false: system %s has no live cor row", text, c, ok, system)
	}
	if c := reqid.CorrelationOf([]string{"bcs.correlation.id=" + text}, ""); !c.IsZero() {
		t.Errorf("CorrelationOf by baggage gave %q for %q, want the zero Correlation", c, text)
	}
	if c := reqid.CorrelationOf(nil, text); !c.IsZero() {
		t.Errorf("CorrelationOf by header gave %q for %q, want the zero Correlation", c, text)
	}
	// A refused baggage member falls through to a valid header, as for any invalid value.
	_, good := corOf(t, "mxe")
	if c := reqid.CorrelationOf([]string{"bcs.correlation.id=" + text}, good); c.String() != good {
		t.Errorf("CorrelationOf(baggage %q, header %q) = %q, want the header's COR", text, good, c)
	}
}

func checkCorAccepted(t *testing.T, system string) {
	t.Helper()
	id, text := corOf(t, system)
	c, ok := reqid.ParseCorrelation(text)
	if !ok || c.String() != text {
		t.Errorf("ParseCorrelation(%q) = (%q, %t), want the same text and true", text, c, ok)
	}
	if n, ok := reqid.NewCorrelation(id); !ok || n != c {
		t.Errorf("NewCorrelation(%q) = (%q, %t), want %q and true", text, n, ok, c)
	}
	if got := reqid.CorrelationOf([]string{"bcs.correlation.id=" + text}, ""); got != c {
		t.Errorf("CorrelationOf by baggage gave %q for %q", got, text)
	}
	if got := reqid.CorrelationOf(nil, text); got != c {
		t.Errorf("CorrelationOf by header gave %q for %q", got, text)
	}
}

func TestEcdCorRefused(t *testing.T) { checkCorRefused(t, "ecd") }

func TestBrdCorRefused(t *testing.T) { checkCorRefused(t, "brd") }

func TestMxeCorAccepted(t *testing.T) { checkCorAccepted(t, "mxe") }

func TestDetachedCorAccepted(t *testing.T) {
	for _, system := range []string{"3ab", "5zz", "7ab"} {
		checkCorAccepted(t, system)
	}
}

// registryCorSystems applies the rule to the vendored registry: the systems not retired whose (system, cor) row is not
// retired.
func registryCorSystems(t *testing.T) []string {
	live := map[string]bool{}
	for _, r := range vectorRows(t, "systems.tsv")[1:] { // the first row is the header
		if len(r) >= 5 && r[4] != "retired" {
			live[r[0]] = true
		}
	}
	var got []string
	for _, r := range vectorRows(t, "kinds.tsv")[1:] {
		if len(r) >= 4 && r[1] == "cor" && r[3] != "retired" && live[r[0]] {
			got = append(got, r[0])
		}
	}
	slices.Sort(got)
	return got
}

func TestCorSystemsMatchRegistry(t *testing.T) {
	want := registryCorSystems(t)
	if !slices.Equal(want, []string{"mxe"}) {
		t.Logf("the registry copy gives %v", want)
	}
	// Probe every registered system of the registry: accepted exactly when the rule says so.
	probes := map[string]bool{}
	for _, r := range vectorRows(t, "systems.tsv")[1:] {
		probes[r[0]] = true
	}
	for system := range probes {
		_, text := corOf(t, system)
		_, ok := reqid.ParseCorrelation(text)
		if wantOK := slices.Contains(want, system); ok != wantOK {
			t.Errorf("the COR of %s is accepted %t, the registry rule says %t (accepted by the rule: %s)", system, ok, wantOK, strings.Join(want, ","))
		}
	}
	for _, system := range want {
		if !probes[system] {
			t.Errorf("%s has a live cor row but no system row", system)
		}
	}
}

func TestMinterCorRefused(t *testing.T) {
	clock := &testClock{}
	clock.ms.Store(1795271400123)
	emq := reqid.NewMinter(testLease(t, "emq:895287301:1795271400000:1795357800000"), clock, newPid(1))
	if c := emq.Correlation(); !c.IsZero() {
		t.Errorf("a minter on an emq lease gave the COR %q, want the zero Correlation: emq has no live cor row", c)
	}
	if id, err := emq.Mint(mustCode(t, "cor")); err != nil || id.System().String() != "emq" {
		t.Fatalf("Mint(cor) on an emq lease = (%v, %v), want an emq id, so the zero Correlation is the rule's refusal", id, err)
	}
	random := &scriptedRandom{chunks: [][]byte{codeDrawn3ab, writerDrawn}}
	detached, err := reqid.NewDetached(random, clock, newPid(7))
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]*reqid.Minter{
		"an mxe lease":      reqid.NewMinter(testLease(t, "mxe:895287301:1795271400000:1795357800000"), clock, newPid(1)),
		"a detached minter": detached,
	} {
		c := m.Correlation()
		if c.IsZero() {
			t.Errorf("%s gave the zero Correlation, want a COR", name)
			continue
		}
		if back, ok := reqid.ParseCorrelation(c.String()); !ok || back != c {
			t.Errorf("%s: the COR %q does not parse back: (%q, %t)", name, c, back, ok)
		}
	}
}
