package main

import "testing"

// #1267: pane targets may be host-qualified so a peer's pane is drivable from its
// card. A bare "%N" must stay local (qualified=false) so the existing send path is
// byte-identical; a "<host>/..../%N" yields the host and the PEER-LOCAL pane id.
func TestSplitPaneHost(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPane string
		wantQual bool
	}{
		{"%8", "", "%8", false},                         // bare local pane — unchanged path
		{"%12", "", "%12", false},                       // bare local pane
		{"local/%8", "local", "%8", true},               // self-qualified → drive here
		{"omarchy/%12", "omarchy", "%12", true},         // host/pane
		{"omarchy/kosaten-0/%3", "omarchy", "%3", true}, // host/seat/pane → peer-local %3
		{"", "", "", false},                             // empty → local no-op
	}
	for _, c := range cases {
		h, p, q := splitPaneHost(c.in)
		if h != c.wantHost || p != c.wantPane || q != c.wantQual {
			t.Errorf("splitPaneHost(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, h, p, q, c.wantHost, c.wantPane, c.wantQual)
		}
	}
}
