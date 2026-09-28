package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManifestCapPreservesLiveTabsAndReportsOmissions(t *testing.T) {
	c := newCollector(nil)
	for i := 0; i < maxManifestTabs; i++ {
		ctx := fmt.Sprint(i)
		c.manifest[ctx] = &tabRec{UID: int64(i + 1), Ctx: ctx, Status: "live"}
	}
	if c.makeManifestRoomLocked() || len(c.manifest) != maxManifestTabs || c.manifestOmitted != 1 {
		t.Fatal("full manifest must not evict a live tab")
	}
	c.manifest["2"].Status = "closed"
	c.manifest["3"].Status = "closed"
	c.dprCache["2"] = 2
	c.vpCache["2"] = [2]float64{1280, 720}
	if !c.makeManifestRoomLocked() || len(c.manifest) != maxManifestTabs-1 || c.manifest["2"] != nil || c.manifest["3"] == nil {
		t.Fatal("must evict the oldest closed tab only")
	}
	if _, ok := c.dprCache["2"]; ok {
		t.Fatal("eviction retained the tab's DPR cache")
	}
	rr := httptest.NewRecorder()
	c.handleManifest(rr, httptest.NewRequest(http.MethodGet, "/manifest", nil))
	var got struct {
		Capacity int    `json:"capacity"`
		Omitted  uint64 `json:"omitted_observations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got.Capacity != maxManifestTabs || got.Omitted != 1 {
		t.Fatalf("manifest omissions must be observable: %+v %v", got, err)
	}
}

func TestFxRelayCapsAndSlowConsumer(t *testing.T) {
	c := newCollector(nil)
	post := func(session string, data []byte) int {
		rr := httptest.NewRecorder()
		c.handleFxChunk(rr, httptest.NewRequest(http.MethodPost, "/fxchunk?session="+session, bytes.NewReader(data)))
		return rr.Code
	}
	if status := post("oversized", make([]byte, maxFxChunkBytes+1)); status != http.StatusRequestEntityTooLarge || len(c.fxRecv) != 0 {
		t.Fatalf("oversized cluster must be rejected without retaining bytes: %d", status)
	}
	for i := 0; i < maxFxStreams; i++ {
		if status := post(fmt.Sprint(i), []byte("init")); status != http.StatusNoContent {
			t.Fatalf("initial relay failed: %d", status)
		}
	}
	if status := post("overflow", []byte("init")); status != http.StatusTooManyRequests || len(c.fxRecv) != maxFxStreams {
		t.Fatalf("session cap not enforced: %d", status)
	}
	c.fxRecv["0"].lastChunk = time.Now().Add(-fxStreamIdle - time.Second)
	if status := post("replacement", []byte("init")); status != http.StatusNoContent || c.fxRecv["0"] != nil {
		t.Fatalf("idle slot was not reclaimed: %d", status)
	}
	s := c.fxRecv["replacement"]
	slow := make(chan []byte, maxFxQueueChunks)
	s.subs[0] = slow
	for i := 0; i <= maxFxQueueChunks; i++ {
		post("replacement", []byte("cluster"))
	}
	if len(s.subs) != 0 {
		t.Fatal("slow WebM subscriber remained subscribed after a dropped cluster")
	}
	for range slow {
		// A disconnected subscriber may drain its bounded queue, then sees EOF.
	}
	for i := 0; i < maxFxSubscribers; i++ {
		s.subs[i] = make(chan []byte, maxFxQueueChunks)
	}
	rr := httptest.NewRecorder()
	c.handleFxStream(rr, httptest.NewRequest(http.MethodGet, "/fxstream?session=replacement", nil))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("subscriber cap not enforced: %d", rr.Code)
	}
	ch := s.subs[0]
	c.dropFxStream("replacement")
	if _, ok := <-ch; ok || c.fxRecv["replacement"] != nil {
		t.Fatal("reset did not release the relay and wake its readers")
	}
}

func TestCaptureWidthBound(t *testing.T) {
	for _, tc := range []struct {
		q    string
		want int
	}{{"", 1280}, {"200", 200}, {"0", 1280}, {"99999999", maxCaptureWidth}} {
		if got := lodWidth(httptest.NewRequest(http.MethodGet, "/shot?w="+tc.q, nil)); got != tc.want {
			t.Errorf("w=%s got %d want %d", tc.q, got, tc.want)
		}
	}
}
