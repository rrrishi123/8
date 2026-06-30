import { useEffect, useRef, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

interface Tab { context: string; url: string; }
type Seeing = 'pixels' | 'channel' | 'request';

// The gravity center: a REAL target seen three ways — pixels (LIVE /stream),
// channel (BiDi DOM), request (classic source). And when INTERACT is on, the
// live stream stops being a mirror and becomes hands: a click on the <img> maps
// to the target's pixels and fires /act (tap), keystrokes fire /act (type). Same
// surface drives a Firefox tab (BiDi) OR a real device (Appium) — one wire.
export function Viewport({ session, title, context: fixedCtx, onAspect, hud, visible, fps: fpsProp, act: actMode, pinned, onPin }:
  { session: string | null; title?: string; context?: string;
    onAspect?: (ratio: number) => void; hud?: { mem?: number; cpu?: number | null }; visible?: boolean; fps?: number; act?: boolean; pinned?: boolean; onPin?: () => void }) {
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
  const streamSrc = session && persistent
    ? `${BASE}/stream?session=${encodeURIComponent(session)}${ctx ? `&context=${encodeURIComponent(ctx)}` : ''}&fps=${effFps}`
    : '';
  useEffect(() => {
    if (!session || !streaming) return;
    let alive = true;
    const cq = ctx ? `&context=${encodeURIComponent(ctx)}` : '';
    const tick = async () => {
      try { const j = await (await fetch(`${BASE}/shot?session=${encodeURIComponent(session)}${cq}`)).json(); if (alive && j.data) { setShot(j.data); if (!persistent) setErr(false); } } catch { if (alive && !persistent) setErr(true); }
    };
    tick();
    // periphery polls at its display rate; the HERO polls SLOWLY (5s) only to seed a
    // freeze-frame fallback for when it pans off-screen — its display stays the live
    // stream, but the cached still means it never blanks either.
    const id = window.setInterval(tick, persistent ? 5000 : Math.max(600, Math.round(1000 / effFps)));
    return () => { alive = false; clearInterval(id); };
  }, [session, ctx, streaming, persistent, effFps]);
  // FIGMA/FIGJAM behaviour: when a seat is off-screen we stop FETCHING (the poll
  // effect gates on `streaming`, the live stream unmounts) — but we keep showing
  // its LAST frame frozen, so panning the board shows what's there, not blanks.
  // No capture cost off-screen; the rendered still is just a cached data-URL.
  const frameSrc = persistent ? streamSrc : (shot || '');

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
  // click on the frame → target pixel coords (ratio × the frame's natural size).
  const onTap = (e: React.MouseEvent<HTMLImageElement>) => {
    if (!interact) return;
    const img = e.currentTarget, r = img.getBoundingClientRect();
    const nx = img.naturalWidth || r.width, ny = img.naturalHeight || r.height;
    act({ action: 'tap', x: Math.round((e.clientX - r.left) / r.width * nx), y: Math.round((e.clientY - r.top) / r.height * ny) });
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
      const nx = img.naturalWidth || r.width, ny = img.naturalHeight || r.height;
      act({ action: 'scroll',
        x: Math.round((cx - r.left) / r.width * nx), y: Math.round((cy - r.top) / r.height * ny),
        x2: Math.round(ax * nx / r.width), y2: Math.round(ay * ny / r.height) });
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
        ? (frameSrc
            ? <div className="vp-stage">
                {/* pinned game-HUD: mem · cpu · fps · resolution — top-left, always on */}
                <div className="vp-hud">
                  {hud?.mem != null && <span className="hud-mem">{hud.mem} MB</span>}
                  {hud?.cpu != null && <span className="hud-cpu">{hud.cpu.toFixed(0)}% cpu</span>}
                  <span className="hud-fps">{effFps} fps</span>
                  {dims && <span className="hud-dim">{dims.w}×{dims.h}</span>}
                </div>
                <img ref={imgRef} key={persistent ? streamSrc : 'poll'} className={`vp-img${interact ? ' vp-interactive' : ''}`} src={frameSrc} alt="live"
                  tabIndex={interact ? 0 : undefined} onClick={onTap} onKeyDown={onKey}
                  onError={() => setErr(true)} onLoad={onFrameLoad} />
              </div>
            : <div className="empty">{!session ? 'no session' : 'paused · off-screen (aperture)'}</div>)
        : <pre className="vp-text">{text || 'reading…'}</pre>}
    </section>
  );
}
