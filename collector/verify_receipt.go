package main

import (
	"strings"
	"sync/atomic"
)

// verify_receipt.go — ALLOCATOR/LEDGER integrity: a verification receipt on close
// (#9757, from %7's "done != verified" audit #9754).
//
// %7 measured the live ledger: of DONE tasks, ~1 in 7 were SELF-closed by their
// own author with no independent check, and the sharpest concentration was
// self-closed OUTWARD tasks (e.g. "tag v0.0.2 on all arms", "DEPLOY what
// shipped"). "done" had drifted loose from "verified", and nothing on the row
// said which it was — you had to re-derive author-vs-closer every time you asked.
//
// This stamps the provenance onto the row at close time so the record answers for
// itself: WHO closed it, WHEN, the WITNESS act-id that anchors the close in the
// ledger (grounded in the record, not a declaration), and whether the closer was
// the author (done, but not a second mind's verification).
//
// It does NOT block a close. Blocking a self-close would break legitimate flows —
// sweeps, auto-reconcile, a mind honestly finishing its own small task — and the
// fix for drift is VISIBILITY, not a wall that pushes people to mislabel. %7's
// standing readout reads SelfClosed per row instead of re-deriving it; this is
// the source-side half that makes that read trustworthy.
//
// Only TASKS are stamped. A RECORD is born done as a message/finding (see
// ledger.go) — "was it verified" is not a question about it — so records pass
// through untouched.
func stampVerifyReceipt(it *workItem, now string) {
	if it == nil || it.Status != "done" {
		return
	}
	if it.Kind == "record" || (it.Kind == "" && isRecord(it.Text)) {
		return
	}
	it.VerifiedAt = now
	it.VerifiedBy = it.FlippedBy
	it.VerifiedAct = atomic.LoadInt64(&actSeq) // the witnessed act counter — the ledger anchor
	it.SelfClosed = isSelfClose(it.By, it.FlippedBy)
}

// isSelfClose — did the AUTHOR close their own task? Compared on stable identity
// TOKENS (uuid hex-prefix and %N pane), reusing the consensus organ's own regexes
// (ackHexRe/ackPaneRe), so "higgs %9" and "%9 (1255da01…)" resolve to the same
// mind — a re-spelled self-close cannot pass itself off as independent. A machine
// close ("auto:*"), a consensus close ("consensus:N-ack"), or a blank closer is
// NOT a self-close: there is no single author identity to match, and we flag only
// what we can prove. %6 != %60: pane tokens match exactly, never as substrings.
func isSelfClose(author, closer string) bool {
	a := strings.ToLower(strings.TrimSpace(author))
	c := strings.ToLower(strings.TrimSpace(closer))
	if a == "" || c == "" {
		return false
	}
	if strings.HasPrefix(c, "auto:") || strings.HasPrefix(c, "consensus:") {
		return false
	}
	at, ct := actorTokens(a), actorTokens(c)
	for k := range at {
		if ct[k] {
			return true
		}
	}
	return false
}

// actorTokens — every stable identity token in a free-form actor string: uuid hex
// runs (keyed "u:"+first8, matching ackKey's canonical prefix) and pane numbers
// (%N). A map so membership is O(1) for the overlap test. The input is expected
// already lowercased by the caller (ackHexRe is lowercase-only).
func actorTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range ackHexRe.FindAllString(s, -1) {
		out["u:"+m[:8]] = true
	}
	for _, m := range ackPaneRe.FindAllString(s, -1) {
		out[m] = true
	}
	return out
}
