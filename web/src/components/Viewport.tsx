import { useEffect, useRef, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

interface Tab { context: string; url: string; }
type Seeing = 'pixels' | 'channel' | 'request';

// The gravity center: a REAL target seen three ways — pixels (LIVE /stream),
// channel (BiDi DOM), request (classic source). And when INTERACT is on, the
// live stream stops being a mirror and becomes hands: a click on the <img> maps
// to the target's pixels and fires /act (tap), keystrokes fire /act (type). Same
// surface drives a Firefox tab (BiDi) OR a real device (Appium) — one wire.
export function Viewport({ session, title, context: fixedCtx, onAspect, hud, visible, live, fps: fpsProp, act: actMode, pinned, onPin, lodW, fx, fxNeedle }:
  { session: string | null; title?: string; context?: string;
    onAspect?: (ratio: number) => void; hud?: { mem?: number; cpu?: number | null }; visible?: boolean; live?: boolean; fps?: number; act?: boolean; pinned?: boolean; onPin?: () => void; lodW?: number; fx?: boolean; fxNeedle?: string }) {
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [ctx, setCtx] = useState(fixedCtx || '');
  const [reqSeat, setReqSeat] = useState('');
  const [seeing, setSeeing] = useState<Seeing>('pixels');
  const [text, setText] = useState(''); // channel DOM / request source
  const [err, setErr] = useState(false);
  const [fps, setFps] = useState(3);
  const [interact, setInteract] = useState(false);
  const [dims, setDims] = useState<{ w: number; h: number } | null>(null);
  const [hidden, setHidden] = useState(false);
  const imgRef = useRef<HTMLImageElement>(null);
  // APERTURE: a screenshot stream is the 38×-cost "see" — running N at once
  // balloons Firefox graphics memory (peaked 20GB once). So only stream a seat
  // that is ON SCREEN (the Canvas computes this from camera geometry — an
  // IntersectionObserver can't see transform-driven pan/zoom) AND while the
  // cockpit tab is focused. Off-screen / backgrounded → <img> unmounts, the
  // capture loop closes. (Also the map/LOD behaviour: pay only for what you see.)
  const streaming = (visible ?? true) && !hidden;
  useEffect(() => {
    const onVis = () => setHidden(document.hidden);
    document.addEventListener('visibilitychange', onVis);
    return () => document.removeEventListener('visibilitychange', onVis);
  }, []);
  // EXPLICIT live, never dynamic: the system must not decide you're "live" — that
  // would make it ambiguous which of your actions are real commands to record. You
  // arm a seat by clicking ✋ live (below), which also PINS it so it streams live
  // and you act on a LIVE view, not a frozen still.
  void actMode;

  // the frame's TRUE pixels size — report its aspect up so the seat box can match
  // the source (a portrait phone ≠ a landscape tab; pretext = natural size).
  const onFrameLoad = (e: React.SyntheticEvent<HTMLImageElement>) => {
    setErr(false);
    const img = e.currentTarget;
    if (img.naturalWidth && img.naturalHeight) {
      setDims({ w: img.naturalWidth, h: img.naturalHeight });
      onAspect?.(img.naturalWidth / img.naturalHeight);
    }
  };

  useEffect(() => {
    if (!session) return;
    if (!fixedCtx) {
      fetch(`${BASE}/tabs?session=${encodeURIComponent(session)}`).then((r) => r.json()).then((j) => {
        const all: Tab[] = j.tabs || [];
        const real = all.filter((t) => !String(t.url || '').includes(location.host)); // not myself
        const list = real.length ? real : all;
        setTabs(list);
        setCtx((cur) => cur || (list[0] ? list[0].context : ''));
      }).catch(() => {});
    }
    fetch(`${BASE}/sessions`).then((r) => r.json())
      .then((j) => setReqSeat((j.sessions || []).find((s: { physics: string; kind: string; id: string }) => s.physics === 'call' && s.kind === 'local')?.id || ''))
      .catch(() => {});
  }, [session, fixedCtx]);

  // pixels is a live stream (no poll); only channel/request poll the source text
  useEffect(() => {
    if (!session || seeing === 'pixels') return;
    let alive = true;
    const cq = ctx ? `&context=${encodeURIComponent(ctx)}` : '';
    async function look() {
      try {
        if (seeing === 'channel') {
          const j = await (await fetch(`${BASE}/source?session=${encodeURIComponent(session!)}${cq}`)).json();
          if (alive) { const n = ((j.value || '').match(/<node/g) || []).length; setText(`channel · BiDi DOM — ${n} nodes\n\n` + String(j.value || '').replace(/></g, '>\n<').slice(0, 6000)); setErr(false); }
        } else {
          if (!reqSeat) { setText('request seat not available'); return; }
          const j = await (await fetch(`${BASE}/source?session=${encodeURIComponent(reqSeat)}`)).json();
          if (alive) { setText(`request · classic source — ${String(j.value || '').length} b\n\n` + String(j.value || '').slice(0, 6000)); setErr(false); }
        }
      } catch { if (alive) setErr(true); }
    }
    let id: number | undefined;
    const start = () => { if (id == null) { look(); id = window.setInterval(look, 8000); } };
    const stop = () => { if (id != null) { clearInterval(id); id = undefined; } };
    const onVis = () => { (document.hidden ? stop : start)(); };
    document.addEventListener('visibilitychange', onVis);
    if (!document.hidden) start();
    return () => { alive = false; stop(); document.removeEventListener('visibilitychange', onVis); };
  }, [session, ctx, seeing, reqSeat]);

  const label = (t: Tab) => { try { return new URL(t.url).host || t.context; } catch { return t.url || t.context; } };
  const effFps = fpsProp ?? fps; // canvas (foveated) drives fps in the seer; selector elsewhere
  // HERO seats hold a persistent MJPEG /stream (live). PERIPHERY seats POLL /shot
  // — a transient request that COMPLETES, freeing the socket — because the browser
  // caps ~6 persistent connections per host: N permanent streams black out the
  // tail seats (youtube, lambdatest never get a connection). Poll keeps it to ~1
  // live stream + cheap stills, so every seat is seen. (Foveation + the cap.)
  const persistent = streaming && effFps >= 2;
  const [shot, setShot] = useState('');
  const lodq = lodW ? `&w=${lodW}` : ''; // LOD: ask for only the pixels we display
  const streamSrc = session && persistent
    ? `${BASE}/stream?session=${encodeURIComponent(session)}${ctx ? `&context=${encodeURIComponent(ctx)}` : ''}&fps=${effFps}${lodq}`
    : '';
  useEffect(() => {
    if (!session) return;
    let alive = true;
    const cq = ctx ? `&context=${encodeURIComponent(ctx)}` : '';
    // FIREFOX stills go through /drawshot (drawSnapshot->JPEG), NOT /shot. BiDi
    // captureScreenshot holds each frame's base64 in Firefox's parent process and
    // never frees it -> the parent climbs ~100MB/min and the watchdog FLOW-10
    // recycles every ~30min (the sawtooth). drawSnapshot is leak-free (proven
    // -3MB over 90 draws). The hero already streams leak-free WebM; this makes the
    // periphery leak-free too, so NO path uses captureScreenshot on Firefox.
    // FIREFOX: render scale follows the role — the HERO renders larger (crisp driving
    // view), PERIPHERY renders tiny (thumbnail). drawSnapshot allocates a surface at this
    // scale, and the parent-process accumulation is ~area — so tiny periphery surfaces let
    // every tile stay LIVE without the memory climb (vs the old full-surface capture).
    const fxScale = persistent ? 0.5 : 0.18;
    const shotUrl = fx
      ? `${BASE}/drawshot?session=${encodeURIComponent(session)}&needle=${encodeURIComponent(fxNeedle || '')}&s=${fxScale}`
      : `${BASE}/shot?session=${encodeURIComponent(session)}${cq}${lodq}`;
    const tick = async () => {
      try { const j = await (await fetch(shotUrl)).json(); if (alive && j.data) { setShot(j.data); if (!persistent) setErr(false); } } catch { if (alive && !persistent) setErr(true); }
    };
    // SEED one still ALWAYS — even off-screen — so zooming out / bird's-eye shows
    // every card's LAST FRAME instead of a blank "paused" placeholder. You asked:
    // "if you zoom out full can you see all the tabs" — now yes, as cached stills.
    tick();
    if (!streaming) return () => { alive = false; }; // off-screen: seed only, no poll cost
    // FOVEATE: on Firefox only the HERO stays live. MEASURED: drawSnapshot accumulates a
    // full-res compositor surface PER DISTINCT TAB (output scale doesn't shrink it), and
    // it only frees on ~6min idle — so live-all-8-tabs climbs ~185MB/min (recycle every
    // ~20min), while hero-only is ~flat (~3MB/min). Periphery freezes on its seed frame
    // until focused. The real fix for live-all is content-side capture (Chrome CDP
    // screencast), which Firefox's drawSnapshot can't do.
    if (fx && !live) return () => { alive = false; }; // not in the live set -> frozen
    const period = fx
      ? (persistent ? Math.max(300, Math.round(1000 / effFps)) : 2000) // hero fluid, live periphery sips
      : (persistent ? 5000 : Math.max(600, Math.round(1000 / effFps)));
    const id = window.setInterval(tick, period);
    return () => { alive = false; clearInterval(id); };
  }, [session, ctx, streaming, persistent, effFps, lodW, fx, fxNeedle, live]);
  // FIGMA/FIGJAM behaviour: when a seat is off-screen we stop FETCHING (the poll
  // effect gates on `streaming`, the live stream unmounts) — but we keep showing
  // its LAST frame frozen, so panning the board shows what's there, not blanks.
  // No capture cost off-screen; the rendered still is just a cached data-URL.
  // Firefox always shows the /drawshot still (leak-free, always renders); other
  // engines use the persistent MJPEG /stream when hero, else their poll still.
  const frameSrc = fx ? (shot || '') : (persistent ? streamSrc : (shot || ''));

  // the hand: POST one /act to this session's target (context only matters for a
  // multi-tab browser; a device session ignores it).
  const act = async (body: Record<string, unknown>) => {
    if (!session) return;
    try {
      await fetch(`${BASE}/act?session=${encodeURIComponent(session)}`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...(ctx ? { context: ctx } : {}), seat: 'ai', ...body }),
      });
      setErr(false);
    } catch { setErr(true); }
  };
  // click on the frame → send a RESOLUTION-INDEPENDENT ratio (0..1) of where in the
  // frame you clicked. The collector resolves it to CSS px via the tab's real
  // viewport. (We can't use the frame's natural size: /drawshot frames are LOD-scaled,
  // so naturalWidth is the downscaled width, not the tab's native resolution.)
  const onTap = (e: React.MouseEvent<HTMLImageElement>) => {
    if (!interact) return;
    const img = e.currentTarget, r = img.getBoundingClientRect();
    act({ action: 'tap', xr: (e.clientX - r.left) / r.width, yr: (e.clientY - r.top) / r.height });
    img.focus();
  };
  // keystrokes -> /act type. WebDriver special keys: Enter \uE007, Backspace \uE003.
  const onKey = (e: React.KeyboardEvent<HTMLImageElement>) => {
    if (!interact) return;
    if (e.key === 'Enter') { e.preventDefault(); act({ action: 'type', text: '\uE007' }); }
    else if (e.key === 'Backspace') { e.preventDefault(); act({ action: 'type', text: '\uE003' }); }
    else if (e.key.length === 1) { e.preventDefault(); act({ action: 'type', text: e.key }); }
  };
  // wheel over a LIVE frame \u2192 /act scroll on the target (and NOT the canvas pan):
  // a native non-passive listener (React onWheel is passive, can't preventDefault),
  // accumulating deltas over ~70ms so one scroll command covers a flick. stop-
  // Propagation keeps the canvas from also panning.
  useEffect(() => {
    const img = imgRef.current;
    if (!img || !interact) return;
    let ax = 0, ay = 0, cx = 0, cy = 0, timer: number | undefined;
    const flush = () => {
      timer = undefined;
      const r = img.getBoundingClientRect();
      // origin as a ratio (like onTap); deltas as raw CSS-px wheel amounts.
      act({ action: 'scroll',
        xr: (cx - r.left) / r.width, yr: (cy - r.top) / r.height,
        x2: Math.round(ax), y2: Math.round(ay) });
      ax = 0; ay = 0;
    };
    const onWheel = (e: WheelEvent) => {
      e.preventDefault(); e.stopPropagation();
      ax += e.deltaX; ay += e.deltaY; cx = e.clientX; cy = e.clientY;
      if (timer == null) timer = window.setTimeout(flush, 70);
    };
    img.addEventListener('wheel', onWheel, { passive: false });
    return () => { img.removeEventListener('wheel', onWheel); if (timer) clearTimeout(timer); };
  }, [interact, persistent, session, ctx, frameSrc]); // eslint-disable-line react-hooks/exhaustive-deps

  // FIREFOX drawSnapshot STREAM via MSE — the LEAK-FREE replacement for the
  // captureScreenshot <img>, for the hero Firefox seat. Starts the pipeline
  // (/fxstart injects the HiddenFrame driver for this tab) and appends the WebM
  // clusters to a SourceBuffer (mode='sequence', trim old buffer — the peer's
  // recipe). Only the hero streams this way; the aperture still pays for one tab.
  // WebM hero RETIRED: the MSE <video> rendered black (mount flakiness) and the
  // continuous full-res encode was the heavy half of the parent-memory climb.
  // Firefox now uses the leak-free /drawshot <img> for hero AND periphery. Kept the
  // pipeline below (disabled) for a future re-enable once <video> mounting is solid.
  const useFx = false && !!fx && persistent && seeing === 'pixels';
  const videoRef = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    if (!useFx || !videoRef.current) return;
    const video = videoRef.current;
    let cancelled = false, reader: ReadableStreamDefaultReader<Uint8Array> | null = null;
    fetch(`${BASE}/fxstart?needle=${encodeURIComponent(fxNeedle || '')}&fps=6`).catch(() => {});
    const ms = new MediaSource();
    video.src = URL.createObjectURL(ms);
    video.load(); // force the element to open the MediaSource -> fires 'sourceopen'
    ms.addEventListener('sourceopen', async () => {
      if (cancelled) return;
      let sb: SourceBuffer;
      try { sb = ms.addSourceBuffer('video/webm;codecs=vp8'); sb.mode = 'sequence'; }
      catch { return; }
      const queue: Uint8Array[] = [];
      const pump = () => { if (!sb.updating && queue.length) { const c = queue.shift(); if (c) try { sb.appendBuffer(c as unknown as BufferSource); } catch { /* wait */ } } };
      sb.addEventListener('updateend', pump);
      try {
        const resp = await fetch(`${BASE}/fxstream?session=${encodeURIComponent(session || 'fox')}`);
        reader = resp.body!.getReader();
        video.play().catch(() => {});
        while (!cancelled) {
          const { done, value } = await reader.read();
          if (done) break;
          if (value && value.length) { queue.push(value); pump(); }
          // trim to bound latency (keep ~15s), the peer's guard against accumulation
          try { if (!sb.updating && video.buffered.length && video.currentTime - video.buffered.start(0) > 20) sb.remove(0, video.currentTime - 15); } catch { /* */ }
        }
      } catch { setErr(true); }
    });
    return () => {
      cancelled = true;
      try { reader?.cancel(); } catch { /* */ }
      fetch(`${BASE}/fxstop`).catch(() => {});
      try { if (ms.readyState === 'open') ms.endOfStream(); } catch { /* */ }
    };
  }, [useFx, fxNeedle, session]);

  return (
    <section className="panel viewport">
      <div className="panel-h">
        <span className={`vp-title${pinned ? ' pinned' : ''}`} onClick={onPin} title={onPin ? 'pin as hero (live)' : undefined}>{pinned ? '★ ' : ''}{title || 'viewport'}</span> {err ? '· ✗' : streaming ? '· ●' : '· ◌'}
        <span className="seeing-tabs">
          {(['pixels', 'channel', 'request'] as Seeing[]).map((s) => (
            <button key={s} className={seeing === s ? 'on' : ''} onClick={() => setSeeing(s)} title={s === 'pixels' ? 'live stream' : s === 'channel' ? 'BiDi DOM' : 'classic source'}>{s}</button>
          ))}
        </span>
        {seeing === 'pixels' && <>
          <button className={`act-toggle${interact ? ' on' : ''}`} onClick={() => setInteract((v) => { const nv = !v; if (nv) onPin?.(); return nv; })} title="arm THIS seat: pin it live + drive it (tap/type/scroll → /act)">{interact ? '✋ live' : '👁 watch'}</button>
          {fpsProp == null
            ? <select className="tab-pick fps-pick" value={fps} onChange={(e) => setFps(Number(e.target.value))} title="stream frame rate">
                {[1, 3, 6, 12].map((f) => <option key={f} value={f}>{f} fps</option>)}
              </select>
            : <span className="fps-foveal" title="foveated: hero seat streams, periphery sips">{effFps >= 3 ? '★ hero' : '· ambient'}</span>}
        </>}
        {!fixedCtx && tabs.length > 0 && (
          <select className="tab-pick" value={ctx} onChange={(e) => setCtx(e.target.value)}>
            {tabs.map((t) => <option key={t.context} value={t.context}>{label(t)}</option>)}
          </select>
        )}
      </div>
      {seeing === 'pixels'
        ? ((frameSrc || useFx)
            ? <div className="vp-stage">
                {/* pinned game-HUD: mem · cpu · fps · resolution — top-left, always on */}
                <div className="vp-hud">
                  {hud?.mem != null && <span className="hud-mem">{hud.mem} MB</span>}
                  {hud?.cpu != null && <span className="hud-cpu">{hud.cpu.toFixed(0)}% cpu</span>}
                  <span className="hud-fps">{useFx ? 'webm' : `${effFps} fps`}</span>
                  {dims && <span className="hud-dim">{dims.w}×{dims.h}</span>}
                </div>
                {useFx
                  ? <video ref={videoRef} className="vp-img vp-video" autoPlay muted playsInline onError={() => setErr(true)} />
                  : <img ref={imgRef} key={persistent ? streamSrc : 'poll'} className={`vp-img${interact ? ' vp-interactive' : ''}`} src={frameSrc} alt="live"
                      tabIndex={interact ? 0 : undefined} onClick={onTap} onKeyDown={onKey}
                      onError={() => setErr(true)} onLoad={onFrameLoad} />}
              </div>
            : <div className="empty">{!session ? 'no session' : 'paused · off-screen (aperture)'}</div>)
        : <pre className="vp-text">{text || 'reading…'}</pre>}
    </section>
  );
}
