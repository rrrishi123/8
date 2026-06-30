import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Viewport } from './Viewport';
import { Resources } from './Resources';
import { PasteCurl } from './PasteCurl';
import { useLocal } from './Dock';
import { procinfo } from '../lib/api';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// pretext spatial cockpit: every live target gets its OWN viewport — each Firefox
// TAB and each real DEVICE is a seat, SIZED TO ITS SOURCE (a portrait phone is
// tall-narrow, a landscape tab is wide-short — aspect read from the live frame),
// laid out in a world you pan/zoom like a map. Each seat wears its own mem/cpu/fps
// HUD pinned top-left, the way a game pins its counters.
interface Seat { id: string; physics: string; hub: string; status: string; stream?: string }
interface Tab { context: string; url: string }
interface Cell { key: string; session: string; context?: string; url?: string; title: string; device?: boolean; gravity?: boolean }
interface PaneRect { id: string; x: number; y: number; w: number; h: number; node: ReactNode; gravity?: boolean }

export function Canvas({ session }: { session: string | null }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [cam, setCam] = useLocal<{ x: number; y: number; z: number }>('cam', { x: 60, y: 30, z: 0.42 });
  const [seats, setSeats] = useState<Seat[]>([]);
  const [tabsBy, setTabsBy] = useState<Record<string, Tab[]>>({});
  // aspects persist: a seat's measured w/h is remembered, so a late first frame
  // doesn't reflow the whole layout out from under you (the "canvas jumps" bug).
  const [aspectBy, setAspectBy] = useLocal<Record<string, number>>('aspectBy', {});
  const [hudBy, setHudBy] = useState<Record<string, { mem?: number; cpu?: number | null }>>({});
  const [actMode, setActMode] = useState(false); // P1·act puts the hero seat into interaction
  const prevCpu = useRef<Record<string, { c: number; t: number }>>({});

  useEffect(() => {
    const load = async () => {
      try {
        const j = await (await fetch(`${BASE}/sessions`)).json();
        const live: Seat[] = ((j.sessions as Seat[]) || []).filter((s) => s.status !== 'disconnected');
        setSeats(live);
        const tb: Record<string, Tab[]> = {};
        await Promise.all(live.filter((s) => s.physics === 'channel').map(async (s) => {
          try {
            const t = await (await fetch(`${BASE}/tabs?session=${encodeURIComponent(s.id)}`)).json();
            tb[s.id] = (t.tabs || []).filter((x: Tab) => !String(x.url || '').includes(location.host));
          } catch { tb[s.id] = []; }
        }));
        setTabsBy(tb);
        // per-tab mem/cpu → HUD map keyed by url (the same data the Resources pane reads)
        const fox = live.find((s) => s.physics === 'channel');
        if (fox) {
          const p = await procinfo(fox.id);
          if (p) {
            const now = Date.now();
            const map: Record<string, { mem?: number; cpu?: number | null }> = {};
            p.tabs.forEach((t) => {
              const pr = prevCpu.current['p' + t.pid];
              prevCpu.current['p' + t.pid] = { c: t.cpu_ms, t: now };
              const cpu = pr && now > pr.t ? Math.max(0, ((t.cpu_ms - pr.c) / (now - pr.t)) * 100) : null;
              map[t.url] = { mem: t.mem_mb, cpu };
            });
            setHudBy(map);
          }
        }
      } catch { /* keep last */ }
    };
    load();
    const t = window.setInterval(() => { if (!document.hidden) load(); }, 4000);
    return () => clearInterval(t);
  }, []);

  const host = (u: string) => { try { return new URL(u).host; } catch { return u || 'tab'; } };

  const cells: Cell[] = [];
  for (const s of seats) {
    if (s.physics === 'channel') {
      for (const tab of tabsBy[s.id] || []) cells.push({ key: s.id + tab.context, session: s.id, context: tab.context, url: tab.url, title: `tab · ${host(tab.url)}` });
    } else {
      const device = !!s.stream;
      cells.push({ key: s.id, session: s.id, device, title: `${device ? 'device · appium' : s.hub?.includes('4444') ? 'firefox · request' : 'seat'} · ${s.id.slice(0, 8)}` });
    }
  }
  cells.forEach((c, i) => { c.gravity = i === 0; });

  // DYNAMIC SIZE: uniform seat height, width = height × the source's aspect ratio
  // (devices default portrait until the first frame measures their true ratio;
  // tabs default landscape). Flow-wrap into a world like tiles on a map.
  const H = 680, GAP = 48, X0 = 120, Y0 = 150, WORLD_W = 2600;
  // APERTURE: a seat streams only when its on-screen rect (after cam transform)
  // overlaps the visible canvas — computed here from geometry because an
  // IntersectionObserver is blind to CSS-transform pan/zoom. Margin avoids edge
  // flicker. This is what stops N screenshot loops ballooning Firefox memory.
  const vpRect = wrap.current?.getBoundingClientRect();
  const onScreen = (x: number, y: number, w: number, h: number) => {
    if (!vpRect) return true;
    const sx = cam.x + x * cam.z, sy = cam.y + y * cam.z, sw = w * cam.z, sh = h * cam.z, m = 120;
    return sx + sw > -m && sx < vpRect.width + m && sy + sh > -m && sy < vpRect.height + m;
  };
  // 1) lay seats out (uniform height, width by aspect, flow-wrap)
  const laid: { c: Cell; x: number; y: number; w: number }[] = [];
  let x = X0, y = Y0;
  for (const c of cells) {
    const aspect = aspectBy[c.key] || (c.device ? 0.46 : 1.6);
    const w = Math.round(H * aspect);
    if (x > X0 && x + w > X0 + WORLD_W) { x = X0; y += H + GAP; }
    laid.push({ c, x, y, w });
    x += w + GAP;
  }
  // 2) FOVEATED aperture (the peer's call): one serialized BiDi socket can't
  // stream N tabs equally, so spend it where attention is — the on-screen seat
  // nearest the viewport CENTER is the HERO (full fps); the rest are ambient
  // (cheap ~0.5fps stills). See everything; see the focus well. No starvation.
  const ccx = vpRect ? vpRect.width / 2 : 0, ccy = vpRect ? vpRect.height / 2 : 0;
  let heroKey = '', heroDist = Infinity;
  const screened = laid.map((L) => {
    const vis = onScreen(L.x, L.y, L.w, H);
    const scx = cam.x + (L.x + L.w / 2) * cam.z, scy = cam.y + (L.y + H / 2) * cam.z;
    const dist = Math.hypot(scx - ccx, scy - ccy);
    if (vis && dist < heroDist) { heroDist = dist; heroKey = L.c.key; }
    return { ...L, vis };
  });
  const seatPanes: PaneRect[] = screened.map((L) => {
    const hero = L.c.key === heroKey;
    return {
      id: 'seat-' + L.c.key, x: L.x, y: L.y, w: L.w, h: H, gravity: hero,
      node: <Viewport session={L.c.session} context={L.c.context} title={L.c.title}
        visible={L.vis} fps={hero ? 6 : 0.2} act={actMode && hero}
        onAspect={(r) => setAspectBy((p) => (Math.abs((p[L.c.key] || 0) - r) > 0.01 ? { ...p, [L.c.key]: r } : p))}
        hud={L.c.url ? hudBy[L.c.url] : undefined} />,
    };
  });
  const yBelow = y + H + GAP + 20;
  const panes: PaneRect[] = [
    ...seatPanes,
    { id: 'resources', x: X0, y: yBelow, w: 620, h: 430, node: <Resources session={session} /> },
    { id: 'compose', x: X0 + 660, y: yBelow, w: 620, h: 430, node: <PasteCurl /> },
  ];
  const worldW = Math.max(2600, X0 + WORLD_W);
  const worldH = yBelow + 520;

  useEffect(() => {
    const el = wrap.current; if (!el) return;
    const onWheel = (e: WheelEvent) => {
      // a wheel over a LIVE (interactive) seat scrolls the TARGET, not the canvas
      if ((e.target as HTMLElement).closest('.vp-interactive')) return;
      e.preventDefault();
      if (e.ctrlKey || e.metaKey) {
        const r = el.getBoundingClientRect(); const mx = e.clientX - r.left, my = e.clientY - r.top;
        setCam((c) => { const nz = Math.max(0.1, Math.min(3, c.z * (e.deltaY < 0 ? 1.06 : 0.94))); const k = nz / c.z; return { z: nz, x: mx - (mx - c.x) * k, y: my - (my - c.y) * k }; });
      } else {
        setCam((c) => ({ ...c, x: c.x - e.deltaX, y: c.y - e.deltaY }));
      }
    };
    const onDown = (e: PointerEvent) => {
      if ((e.target as HTMLElement).closest('button, input, select, textarea, a, .seeing-tabs, .tab-pick, .series-row, .rec-btn, .curl-in, .persp-bar, .vp-interactive')) return;
      el.style.cursor = 'grabbing';
      let lx = e.clientX, ly = e.clientY;
      const move = (ev: PointerEvent) => { setCam((c) => ({ ...c, x: c.x + (ev.clientX - lx), y: c.y + (ev.clientY - ly) })); lx = ev.clientX; ly = ev.clientY; };
      const up = () => { el.style.cursor = ''; window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up); };
      window.addEventListener('pointermove', move); window.addEventListener('pointerup', up);
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    el.addEventListener('pointerdown', onDown);
    return () => { el.removeEventListener('wheel', onWheel); el.removeEventListener('pointerdown', onDown); };
  }, [setCam]);
  const goto = (rx: number, ry: number, rw: number, rh: number, z: number) => { const r = wrap.current!.getBoundingClientRect(); setCam({ z, x: r.width / 2 - (rx + rw / 2) * z, y: r.height / 2 - (ry + rh / 2) * z }); };
  const persp = {
    p1: () => { setActMode(true); const g = seatPanes.find((p) => p.gravity) || seatPanes[0]; if (g) goto(g.x, g.y, g.w, g.h, 0.9); },
    p2: () => { setActMode(false); goto(X0, Y0, WORLD_W, H, 0.45); },
    bird: () => { setActMode(false); goto(0, 100, worldW, worldH, 0.3); },
  };

  return (
    <div className="canvas-wrap" ref={wrap}>
      <div className="world" style={{ transform: `translate(${cam.x}px,${cam.y}px) scale(${cam.z})` }}>
        {panes.map((p) => (
          <div key={p.id} className={`cbox${p.gravity ? ' gravity' : ''}`} style={{ left: p.x, top: p.y, width: p.w, height: p.h }}>
            {p.node}
          </div>
        ))}
      </div>
      <div className="persp-bar">
        <button onClick={persp.p1} title="one seat">P1 · act</button>
        <button onClick={persp.p2} title="all seats, side by side">P2 · seats</button>
        <button onClick={persp.bird} title="see everything">◇ bird's-eye</button>
        <span className="persp-z">{cells.length} seats · {Math.round(cam.z * 100)}%</span>
      </div>
    </div>
  );
}
