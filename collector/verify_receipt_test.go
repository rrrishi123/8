package main

import "testing"

func TestIsSelfClose(t *testing.T) {
	cases := []struct {
		name, author, closer string
		want                 bool
	}{
		{"same pane, different spelling", "higgs %9", "%9 (aaaaaaaa-1111-2222-3333-444444444444)", true},
		{"same uuid prefix, different form", "%8 (bbbbbbbb-5555-6666-7777-888888888888)", "bbbbbbbb", true},
		{"different panes are independent", "conductor %6", "sibling-1 %7", false},
		{"auto close is not a self-close", "higgs %9", "auto:stale", false},
		{"consensus close is not a self-close", "%6 (12444bc8)", "consensus:5-ack", false},
		{"swept self-close still matches", "higgs %9", "higgs %9 (swept)", true},
		{"blank closer", "higgs %9", "", false},
		{"blank author", "", "higgs %9", false},
		{"%6 is not %60 (exact pane token)", "x %6", "y %60", false},
	}
	for _, c := range cases {
		if got := isSelfClose(c.author, c.closer); got != c.want {
			t.Errorf("%s: isSelfClose(%q,%q)=%v want %v", c.name, c.author, c.closer, got, c.want)
		}
	}
}

func TestStampVerifyReceipt(t *testing.T) {
	// a task self-closed by its author: stamped, SelfClosed true, anchored.
	self := &workItem{ID: 1, Text: "do the thing", Status: "done", By: "higgs %9", FlippedBy: "higgs %9", Kind: "task"}
	stampVerifyReceipt(self, "2026-11-25T00:00:00Z")
	if self.VerifiedAt == "" || self.VerifiedBy != "higgs %9" || !self.SelfClosed {
		t.Fatalf("self-closed task not stamped as expected: %+v", self)
	}

	// a task closed by a DIFFERENT mind: stamped, SelfClosed false (independently verified).
	indep := &workItem{ID: 2, Text: "do the thing", Status: "done", By: "higgs %9", FlippedBy: "sibling-1 %7", Kind: "task"}
	stampVerifyReceipt(indep, "2026-11-25T00:00:00Z")
	if indep.VerifiedAt == "" || indep.SelfClosed {
		t.Fatalf("independent close must be stamped and not self-closed: %+v", indep)
	}

	// a RECORD is born done as a message — never a verification question, never stamped.
	rec := &workItem{ID: 3, Text: "[FINDING] something true", Status: "done", By: "higgs %9", FlippedBy: "higgs %9", Kind: "record"}
	stampVerifyReceipt(rec, "2026-11-25T00:00:00Z")
	if rec.VerifiedAt != "" || rec.SelfClosed {
		t.Fatalf("a record must not be stamped: %+v", rec)
	}

	// a legacy (blank-kind) record detected by prefix is likewise not stamped.
	legacyRec := &workItem{ID: 4, Text: "FINDING: measured drift", Status: "done", By: "a %1", FlippedBy: "a %1"}
	stampVerifyReceipt(legacyRec, "2026-11-25T00:00:00Z")
	if legacyRec.VerifiedAt != "" {
		t.Fatalf("a legacy record (prefix-detected) must not be stamped: %+v", legacyRec)
	}

	// a non-done status is never stamped.
	doing := &workItem{ID: 5, Text: "x", Status: "doing", By: "a %1", FlippedBy: "b %2", Kind: "task"}
	stampVerifyReceipt(doing, "2026-11-25T00:00:00Z")
	if doing.VerifiedAt != "" {
		t.Fatalf("a non-done task must not be stamped: %+v", doing)
	}
}
