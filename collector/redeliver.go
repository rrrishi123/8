package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// WATCHER STALE-SWEEP — detector-first watcher resilience (#9922/#9923,
// synthesizes pmf %13 #9920).
//
// A watcher (e.g. %29's regression-watch) drops a deliverable SILENTLY when it
// announces work then dies inside its single session: the work lived in that one
// mind's head, never on the shared surface, so nothing noticed it was undone.
// The fix is detector-first — file each deliverable as a CLAIMABLE item with a
// Deadline (the opt-in marker), have the owning mind HEARTBEAT it while alive
// (POST {id,beat:true}), and sweep the ones that went silent back into the pool
// for the EXISTING allocator / inbox to re-route to a fresh idle non-author mind.
// A dropped deliverable thus becomes a re-queued, witnessed, picked-up task
// instead of a silent miss — without any new push-wake (/spawn deferred).
//
// CORRECTNESS (pmf %13 #9939), the two traps a naive sweep would fall into:
//
//  1. RE-QUEUE THE DEAD, NOT THE SLOW. A real RD/VD run legitimately takes long;
//     re-queuing on a wall-clock deadline would double-run a still-alive task. So
//     liveness is read ONLY from the heartbeat (latestTouch), never the deadline:
//     a beating owner is working, whatever the clock says, and re-routing it would
//     just run the same job twice. The Deadline field is the opt-in marker (and a
//     human-facing "expected by"), not a staleness trigger.
//
//  2. FIRE ONCE PER DROP. The sweep acts ONLY on `doing` items and re-queues to
//     `todo`; a todo is the allocator/inbox's domain, not the sweep's, so the
//     same drop can never be swept twice. If a re-routed mind also dies, the item
//     is `doing` and silent again — swept again — which is correct, not a loop.

// deliverableStale reports whether a watched deliverable has gone silent: it is
// opted in (Deadline set), a real task, still `doing`, carries no receipt, and
// its owner has not touched it (status / delivery / heartbeat) within grace.
// Liveness is the heartbeat, not the clock — a beating owner is never stale.
func deliverableStale(it workItem, now time.Time, grace time.Duration) bool {
	if it.Deadline == "" || it.Kind == "record" || it.Status != "doing" || it.Receipt != "" {
		return false
	}
	last := latestTouch(it)
	return !last.IsZero() && now.Sub(last) > grace
}

// latestTouch is the most recent sign of life on an item: an explicit heartbeat,
// the delivery stamp, the doing-ack, or the last status change — newest wins.
func latestTouch(it workItem) time.Time {
	var t time.Time
	for _, s := range []string{it.BeatAt, it.AckedAt, it.DeliveredAt, it.TS} {
		if s == "" {
			continue
		}
		if p, err := time.Parse(time.RFC3339, s); err == nil && p.After(t) {
			t = p
		}
	}
	return t
}

// pickStaleDeliverables returns the indices of items to re-queue this pass (pure,
// so the sweep's decision is testable without a ledger or a clock).
func pickStaleDeliverables(items []workItem, now time.Time, grace time.Duration) []int {
	var idx []int
	for i := range items {
		if deliverableStale(items[i], now, grace) {
			idx = append(idx, i)
		}
	}
	return idx
}

// redeliverPass — one sweep. A silent deliverable is re-queued: status->todo,
// assignee + delivery + heartbeat stamps cleared so the allocator / inbox
// re-offer it to a fresh idle non-author mind, FlippedBy records the sweep, and a
// witnessed record is written so the drop is on the shared surface, not lost.
// Returns how many were re-queued (0 = silence). The caller budget-gates it.
func (c *collector) redeliverPass(grace time.Duration) int {
	c.tmu.Lock()
	var items []workItem
	b, err := os.ReadFile(workFile())
	if err != nil || json.Unmarshal(b, &items) != nil {
		c.tmu.Unlock()
		return 0
	}
	stale := pickStaleDeliverables(items, time.Now(), grace)
	if len(stale) == 0 {
		c.tmu.Unlock()
		return 0
	}
	var max int64
	for _, it := range items {
		if it.ID > max {
			max = it.ID
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var requeued []workItem
	var records []workItem
	for _, i := range stale {
		it := &items[i]
		was := it.Assignee
		it.Status = "todo"
		it.Assignee = ""
		it.DeliveredAt, it.DeliveredTo = "", ""
		it.AckedAt, it.AckedBy = "", ""
		it.BeatAt = ""
		it.FlippedBy = "sweep:stale-redeliver"
		it.TS = now
		requeued = append(requeued, *it)
		max++
		records = append(records, workItem{ID: max, Kind: "record", Status: "done", By: "sweep", TS: now, Epoch: currentEpoch(),
			Text: fmt.Sprintf("[sweep] re-queued deliverable #%d: owner %q went silent past grace with no receipt — back to the pool for re-route (#9923 watcher-resilience)", it.ID, was)})
	}
	items = append(items, records...)
	writeWork(items)
	c.tmu.Unlock()

	for _, it := range requeued {
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.redeliver","params":{"id":%d,"reason":"stale-no-receipt"}}}`, it.ID))
	}
	return len(requeued)
}
