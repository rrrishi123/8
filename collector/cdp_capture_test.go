package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func lodTestJPEG() []byte {
	im := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			im.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
		}
	}
	var out bytes.Buffer
	_ = jpeg.Encode(&out, im, &jpeg.Options{Quality: 50})
	return out.Bytes()
}

func TestCDPShotScalesInBrowserAndPreservesJPEG(t *testing.T) {
	frame := base64.StdEncoding.EncodeToString(lodTestJPEG())
	var captured bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			fmt.Fprint(w, `{}`)
			return
		}
		var in struct {
			Method, SessionID string
			Params            struct {
				Clip                  map[string]float64
				CaptureBeyondViewport bool
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch in.Method {
		case "Target.getTargets":
			fmt.Fprint(w, `{"result":{"targetInfos":[{"targetId":"LOD-TAB","type":"page","url":"https://example.test"}]}}`)
		case "Target.attachToTarget":
			fmt.Fprint(w, `{"result":{"sessionId":"LOD-SESSION"}}`)
		case "Page.getLayoutMetrics":
			if in.SessionID != "LOD-SESSION" {
				t.Error("viewport metrics read from the wrong target")
			}
			fmt.Fprint(w, `{"result":{"cssVisualViewport":{"pageX":10,"pageY":100,"clientWidth":800,"clientHeight":400,"scale":1,"zoom":1.25}}}`)
		case "Runtime.evaluate":
			if in.SessionID != "LOD-SESSION" {
				t.Error("DPR read from the wrong target")
			}
			fmt.Fprint(w, `{"result":{"result":{"value":2.5}}}`)
		case "Page.captureScreenshot":
			captured = true
			clip := in.Params.Clip
			if in.SessionID != "LOD-SESSION" || clip["scale"] != 0.1 || clip["width"] != 1000 || clip["height"] != 500 || clip["x"] != 12.5 || clip["y"] != 125 || in.Params.CaptureBeyondViewport {
				t.Errorf("wrong native clip: %+v", in)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]string{"data": frame}})
		case "Target.detachFromTarget":
			fmt.Fprint(w, `{"result":{}}`)
		default:
			t.Errorf("unexpected command: %s", in.Method)
		}
	}))
	defer server.Close()
	c := newCollector([]broker{{id: "lod-test", base: server.URL}})
	rr := httptest.NewRecorder()
	c.handleShot(rr, httptest.NewRequest(http.MethodGet, "/shot?session=lod-test&context=LOD-TAB&w=200", nil))
	var got struct{ Data string }
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || rr.Code != 200 || !captured {
		t.Fatalf("capture failed: %s (%v)", rr.Body.String(), err)
	}
	if got.Data != "data:image/jpeg;base64,"+frame {
		t.Fatal("CDP JPEG was transformed in the collector instead of passed through")
	}
}

// Explicit opt-in: the supplied broker must hold a disposable test page. This
// test changes that page's emulation/document only; it never opens user tabs.
func TestCDPLODLive(t *testing.T) {
	endpoint := os.Getenv("EIGHT_TEST_CDP_BROKER")
	if endpoint == "" {
		t.Skip("set EIGHT_TEST_CDP_BROKER to a disposable page broker")
	}
	c := newCollector(nil)
	c.client.Timeout = 10 * time.Second
	b := broker{id: "lod-live", base: endpoint}
	defer c.command(&b, `{"method":"Emulation.clearDeviceMetricsOverride"}`)
	for _, dpr := range []int{1, 2} {
		cmd := fmt.Sprintf(`{"method":"Emulation.setDeviceMetricsOverride","params":{"width":1280,"height":720,"deviceScaleFactor":%d,"mobile":false}}`, dpr)
		if _, err := c.command(&b, cmd); err != nil {
			t.Fatal(err)
		}
		_, err := c.command(&b, `{"method":"Runtime.evaluate","params":{"expression":"document.body.style.cssText='margin:0;background:linear-gradient(45deg,#348,#c85);height:3000px';document.body.innerHTML='<h1>Codex LOD fixture</h1>';window.scrollTo(0,400)","returnByValue":true}}`)
		if err != nil {
			t.Fatal(err)
		}
		for _, width := range []int{160, 320, 1280} {
			sr, err := c.cdpShot(&b, "", width)
			if err != nil {
				t.Fatal(err)
			}
			var shot struct{ Result struct{ Data string } }
			_ = json.Unmarshal(sr, &shot)
			raw, _ := base64.StdEncoding.DecodeString(shot.Result.Data)
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
			if err != nil || cfg.Width > width || cfg.Width < width-16 {
				t.Fatalf("DPR=%d requested=%d got=%dx%d err=%v reply=%s", dpr, width, cfg.Width, cfg.Height, err, firstN(string(sr), 200))
			}
			t.Logf("DPR=%d requested=%d got=%dx%d bytes=%d", dpr, width, cfg.Width, cfg.Height, len(raw))
		}
	}
}

var lodBenchmarkSink string

func BenchmarkCDPJPEGDelivery(b *testing.B) {
	full := base64.StdEncoding.EncodeToString(lodTestJPEG())
	// The new path receives a frame already sized by Chrome; model the same
	// requested width so payload-copy costs are included in both measurements.
	small := shrinkTo(full, 200)
	b.Run("collector_resize_800_to_200", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			lodBenchmarkSink = "data:image/jpeg;base64," + shrinkTo(full, 200)
		}
	})
	b.Run("native_size_passthrough_200", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			lodBenchmarkSink = "data:image/jpeg;base64," + small
		}
	})
}

func TestCDPCaptureRejectsMissingMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"unsupported"}}`)
	}))
	defer server.Close()
	c := newCollector(nil)
	_, err := c.cdpCapture(&broker{base: server.URL}, "", 320)
	if err == nil || !strings.Contains(err.Error(), "viewport") {
		t.Fatalf("missing metrics must fail rather than capture full-size: %v", err)
	}
}
