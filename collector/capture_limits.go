package main

import "time"

const (
	maxCaptureWidth    = 4096
	maxManifestTabs    = 4096
	maxSurfaceIDBytes  = 512
	maxSurfaceURLBytes = 8192
	maxFxStreams       = 4
	maxFxSubscribers   = 4
	maxFxQueueChunks   = 2
	maxFxChunkBytes    = 1 << 20
	fxStreamIdle       = 2 * time.Minute
)

// Caller holds fxMu. Idle unobserved sessions release their init segment.
func (c *collector) pruneFxStreamsLocked(now time.Time) {
	for id, s := range c.fxRecv {
		s.mu.Lock()
		idle := len(s.subs) == 0 && now.Sub(s.lastChunk) > fxStreamIdle
		s.mu.Unlock()
		if idle {
			delete(c.fxRecv, id)
		}
	}
}

func (c *collector) dropFxStream(id string) {
	c.fxMu.Lock()
	defer c.fxMu.Unlock()
	if s := c.fxRecv[id]; s != nil {
		s.mu.Lock()
		for key, ch := range s.subs {
			close(ch)
			delete(s.subs, key)
		}
		s.mu.Unlock()
		delete(c.fxRecv, id)
	}
}

// makeManifestRoomLocked evicts the oldest closed record, never a live tab.
// At a fully live cap, reject new observations and expose an omission counter.
func (c *collector) makeManifestRoomLocked() bool {
	if len(c.manifest) < maxManifestTabs {
		return true
	}
	oldest := ""
	for key, rec := range c.manifest {
		if rec.Status == "closed" && (oldest == "" || rec.UID < c.manifest[oldest].UID) {
			oldest = key
		}
	}
	if oldest != "" {
		delete(c.manifest, oldest)
		c.dmu.Lock()
		delete(c.dprCache, oldest)
		delete(c.vpCache, oldest)
		c.dmu.Unlock()
		return true
	}
	c.manifestOmitted++
	return false
}
