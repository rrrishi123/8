import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Viewport } from './Viewport';
import { Resources } from './Resources';
import { PasteCurl } from './PasteCurl';
import { useLocal } from './Dock';
import { procinfo, recordCtl, listSeries, replaySeries, addTab, type SeriesInfo, type CapFrame } from '../lib/api';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// pretext spatial cockpit: every live target is a card SIZED TO ITS SOURCE, laid
// out in a world you pan/zoom like a map. A browser's tabs are a SOLITAIRE DECK —
// stacked cards you fan out to see together; a device/request seat is a lone card.
// Everything you do — driving a tab, ADDING a tab, RECORDING a case — happens IN
// this canvas (pretext's whole point: it doesn't escape to another page).
interface Seat { id: string; physics: string; hub: string; status: string; stream?: string }
interface Tab { context: string; url: string }
interface Cell { key: string; session: string; context?: string; url?: string; title: string; device?: boolean }
interface Stack { key: string; session: string; isBrowser: boolean; isCDP: boolean; label: string; cells: Cell[] }
interface PaneRect { id: string; x: number; y: number; w: number; h: number; z?: number; node: ReactNode; gravity?: boolean }

export function Canvas({ session }: { session: string | null }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [cam, setCam] = useLocal<{ x: number; y: number; z: number }>('cam', { x: 60, y: 30, z: 0.42 });
  const [seats, setSeats] = useState<Seat[]>([]);
  const [tabsBy, setTabsBy] = useState<Record<string, Tab[]>>({});
  const [aspectBy, setAspectBy] = useLocal<Record<string, number>>('aspectBy', {});
  const [hudBy, setHudBy] = useState<Record<string, { mem?: number; cpu?: number | null }>>({});
  const [pinnedKey, setPinnedKey] = useState(''); // the HERO card (explicit pin, not center)
  const [spreadBy, setSpreadBy] = useLocal<Record<string, boolean>>('spreadBy', {}); // fanned decks
  const prevCpu = useRef<Record<string, { c: number; t: number }>>({});
  // RECORD → REPLAY, shown IN the canvas: every /act you drive on a live seat is
  // captured (seat-attributed); the captured commands appear as a live deck right
  // here, not on the default feed page — replay re-fires the whole series.
  const [rec, setRec] = useState<{ recording: boolean; name?: string; frames?: number; captured?: CapFrame[] }>({ recording: false });
  const [series, setSeries] = useState<SeriesInfo[]>([]);
  const [recName, setRecName] = useState('canvas-1');
  // idle 8 should be QUIET: poll /record fast ONLY while recording (the deck needs
  // it live), slow otherwise; /series only every 8s (it changes only on save). This
  // is why "idle 8 was moving" — it was hammering /record+/series every 1.2s.
  const recRef = useRef(false);
  useEffect(() => {
    let alive = true;
    const tick = async () => {
      if (!alive) return;
      if (!document.hidden) {
        const r = await recordCtl(''); setRec(r); recRef.current = !!r.recording;
      }
      if (alive) window.setTimeout(tick, recRef.current ? 1000 : 4000);
    };
    tick();
    listSeries().then(setSeries);
    const st = window.setInterval(() => { if (!document.hidden) listSeries().then(setSeries); }, 8000);
    return () => { alive = false; clearInterval(st); };
  }, []);
  const toggleRec = async () => {
    if (rec.recording) await recordCtl('stop'); else await recordCtl('start', recName, 'ai');
    recordCtl('').then(setRec); listSeries().then(setSeries);
  };

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
        const fox = live.find((s) => s.physics === 'channel' && s.stream !== 'cdp');
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

  const host = (u: string) => { try { return new URL(u).host.replace(/^www\./, ''); } catch { return u || 'tab'; } };

  // ── group seats into solitaire decks ─────────────────────────────────────────
  const stacks: Stack[] = [];
  for (const s of seats) {
    if (s.physics === 'channel') {
      const cells: Cell[] = (tabsBy[s.id] || []).map((tab) => ({ key: s.id + tab.context, session: s.id, context: tab.context, url: tab.url, title: host(tab.url) }));
      const label = s.stream === 'cdp' ? 'chrome' : s.id === 'fox' ? 'firefox' : s.id;
      stacks.push({ key: s.id, session: s.id, isBrowser: true, isCDP: s.stream === 'cdp', label, cells });
    } else {
      const device = !!s.stream;
      const label = device ? 'device' : s.hub?.includes('4444') ? 'firefox · request' : 'seat';
      stacks.push({ key: s.id, session: s.id, isBrowser: false, isCDP: false, label, cells: [{ key: s.id, session: s.id, device, title: `${label} · ${s.id.slice(0, 8)}` }] });
    }
  }
  const cells: Cell[] = stacks.flatMap((st) => st.cells);
  const heroKey = (pinnedKey && cells.some((c) => c.key === pinnedKey)) ? pinnedKey : (cells[0]?.key || '');

  // ── layout ───────────────────────────────────────────────────────────────────
  const H = 680, GAP = 72, X0 = 120, Y0 = 175, WORLD_W = 2800, HEADER = 44, OFFX = 32, OFFY = 36;
  const vpRect = wrap.current?.getBoundingClientRect();
  const onScreen = (x: number, y: number, w: number, h: number) => {
    if (!vpRect) return true;
    const sx = cam.x + x * cam.z, sy = cam.y + y * cam.z, sw = w * cam.z, sh = h * cam.z, m = 120;
    return sx + sw > -m && sx < vpRect.width + m && sy + sh > -m && sy < vpRect.height + m;
  };
  const cardW = (c: Cell) => Math.round(H * (aspectBy[c.key] || (c.device ? 0.46 : 1.6)));

  interface Laid { c: Cell; x: number; y: number; w: number; z: number; top: boolean }
  const laid: Laid[] = [];
  const decks: { st: Stack; x: number; y: number; w: number; spread: boolean }[] = [];
  let x = X0, y = Y0;
  const rowH = H + HEADER + OFFY * 5 + GAP;
  for (const st of stacks) {
    const spread = !!spreadBy[st.key] || st.cells.length <= 1;
    const w0 = cardW(st.cells[0] || ({ key: '', session: '' } as Cell)) || 300;
    const fpW = spread ? Math.max(w0, st.cells.reduce((a, c) => a + cardW(c) + 16, -16)) : w0 + OFFX * (st.cells.length - 1);
    if (x > X0 && x + fpW > X0 + WORLD_W) { x = X0; y += rowH; }
    const ox = x, oy = y + HEADER;
    const topKey = st.cells.some((c) => c.key === heroKey) ? heroKey : st.cells[0]?.key;
    const others = st.cells.filter((c) => c.key !== topKey);
    let sx = ox;
    st.cells.forEach((c) => {
      if (spread) { laid.push({ c, x: sx, y: oy, w: cardW(c), z: 1, top: c.key === topKey }); sx += cardW(c) + 16; }
      else {
        const isTop = c.key === topKey;
        const oi = isTop ? 0 : others.indexOf(c) + 1;
        const z = isTop ? others.length + 2 : others.length - others.indexOf(c);
        laid.push({ c, x: ox + OFFX * oi, y: oy + OFFY * oi, w: w0, z, top: isTop });
      }
    });
    decks.push({ st, x: ox, y, w: fpW, spread });
    x = ox + fpW + GAP;
  }

  const screened = laid.map((L) => ({ ...L, vis: onScreen(L.x, L.y, L.w, H) }));
  const seatPanes: PaneRect[] = screened.map((L) => {
    const hero = L.c.key === heroKey;
    const streams = L.top && L.vis; // only the deck's TOP card spends the socket
    return {
      id: 'seat-' + L.c.key, x: L.x, y: L.y, w: L.w, h: H, z: L.z, gravity: hero,
      node: <Viewport session={L.c.session} context={L.c.context} title={L.c.title}
        visible={streams} fps={hero ? 3 : (streams ? 0.4 : 0)} pinned={hero}
        onPin={() => setPinnedKey(L.c.key)}
        onAspect={(r) => setAspectBy((p) => (Math.abs((p[L.c.key] || 0) - r) > 0.01 ? { ...p, [L.c.key]: r } : p))}
        hud={L.c.url ? hudBy[L.c.url] : undefined} />,
    };
  });

  const yBelow = y + rowH + 10;
  // the RECORDING DECK — captured commands live, in place (not the default feed).
  const recPane: PaneRect = {
    id: 'recording', x: X0, y: yBelow, w: 640, h: 470, node: (
      <div className="rec-deck">
        <div className="panel-h">recording {rec.recording ? `· ● ${rec.name} · ${rec.frames ?? 0} cmds` : `· ○ idle`}</div>
        <div className="rec-deck-body">
          {!(rec.captured && rec.captured.length) && <div className="empty">{rec.recording ? 'drive a live card — each command lands here as you go' : 'press ● record, then drive a card; the case builds here'}</div>}
          {(rec.captured || []).slice().reverse().map((f) => (
            <div key={f.seq} className={`cap-card ${f.physics}`} title={f.url}>
              <span className="cap-seq">{f.seq}</span>
              <span className={`cap-phys ${f.physics}`}>{f.physics === 'channel' ? '⟂' : '→'}</span>
              <span className="cap-method">{f.method}</span>
              <span className="cap-url">{host(f.url)}</span>
              {f.seat && <span className="cap-seat">{f.seat}</span>}
              <span className={`cap-status s${Math.floor((f.status || 0) / 100)}`}>{f.status || '·'}</span>
            </div>
          ))}
        </div>
      </div>
    ),
  };
  const panes: PaneRect[] = [
    ...seatPanes,
    recPane,
    { id: 'resources', x: X0 + 680, y: yBelow, w: 560, h: 470, node: <Resources session={session} /> },
    { id: 'compose', x: X0 + 1260, y: yBelow, w: 560, h: 470, node: <PasteCurl /> },
  ];
  const worldW = Math.max(2800, x);
  const worldH = yBelow + 540;

  useEffect(() => {
    const el = wrap.current; if (!el) return;
    const onWheel = (e: WheelEvent) => {
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
      if ((e.target as HTMLElement).closest('button, input, select, textarea, a, .seeing-tabs, .tab-pick, .series-row, .rec-btn, .curl-in, .persp-bar, .deck-head, .cap-card, .vp-interactive')) return;
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
    p1: () => { const g = seatPanes.find((p) => p.gravity) || seatPanes[0]; if (g) goto(g.x, g.y, g.w, g.h, 0.9); },
    p2: () => goto(X0, Y0, WORLD_W, H + HEADER, 0.45),
    bird: () => goto(0, 100, worldW, worldH, 0.3),
  };

  const newTab = async (st: Stack) => {
    const url = window.prompt(`new tab in ${st.label} — url or task target (e.g. airbnb.com, amazon.com):`, 'https://www.airbnb.com');
    if (url == null) return;
    await addTab(st.session, url.trim(), st.isCDP);
  };

  return (
    <div className="canvas-wrap" ref={wrap}>
      <div className="world" style={{ transform: `translate(${cam.x}px,${cam.y}px) scale(${cam.z})` }}>
        {/* deck headers — name · tab count · fan · add tab */}
        {decks.map((d) => (
          <div key={'h-' + d.st.key} className="deck-head" style={{ left: d.x, top: d.y, width: d.w }}>
            <span className={`deck-name ${d.st.isCDP ? 'chrome' : d.st.isBrowser ? 'firefox' : 'seat'}`}>{d.st.label}</span>
            {d.st.isBrowser && <span className="deck-count">{d.st.cells.length} tab{d.st.cells.length === 1 ? '' : 's'}</span>}
            {d.st.isBrowser && d.st.cells.length > 1 && (
              <button className="deck-btn" title={d.spread ? 'stack the deck' : 'fan the deck out — see all tabs'}
                onClick={() => setSpreadBy((p) => ({ ...p, [d.st.key]: !p[d.st.key] }))}>{d.spread ? '▣ stack' : '⊞ fan'}</button>
            )}
            {d.st.isBrowser && <button className="deck-btn add" title="open a new tab in this browser to drive/record" onClick={() => newTab(d.st)}>+ tab</button>}
          </div>
        ))}
        {panes.map((p) => (
          <div key={p.id} className={`cbox${p.gravity ? ' gravity' : ''}`} style={{ left: p.x, top: p.y, width: p.w, height: p.h, zIndex: p.z ?? 'auto' }}>
            {p.node}
          </div>
        ))}
      </div>
      {/* RECORD → REPLAY control (the deck itself lives in the world above) */}
      <div className="rec-bar">
        <button className={`rec-btn${rec.recording ? ' on' : ''}`} onClick={toggleRec} title="record every /act you drive on a live card; replay re-fires them — deterministic">
          {rec.recording ? `● REC ${rec.name} · ${rec.frames ?? 0} cmds` : '○ record'}
        </button>
        {!rec.recording && <input className="rec-in" value={recName} onChange={(e) => setRecName(e.target.value)} title="case name" />}
        {series.length > 0 && (
          <select className="rec-in" value="" onChange={(e) => { if (e.target.value) replaySeries(e.target.value); e.currentTarget.value = ''; }} title="replay a saved case">
            <option value="">▶ replay…</option>
            {series.map((s) => <option key={s.name} value={s.name}>{s.name} · {s.frames} cmds</option>)}
          </select>
        )}
      </div>
      <div className="persp-bar">
        <button onClick={persp.p1} title="one card">P1 · act</button>
        <button onClick={persp.p2} title="all decks, side by side">P2 · decks</button>
        <button onClick={persp.bird} title="see everything">◇ bird's-eye</button>
        <span className="persp-z">{stacks.length} decks · {cells.length} cards · {Math.round(cam.z * 100)}%</span>
      </div>
      {vpRect && worldW > 0 && (
        <svg className="minimap" viewBox={`0 0 ${worldW} ${worldH}`}
          style={{ width: 220, height: Math.round(Math.min(170, 220 * worldH / worldW)) }}
          onClick={(e) => {
            const r = (e.currentTarget as SVGSVGElement).getBoundingClientRect();
            const wx = (e.clientX - r.left) / r.width * worldW, wy = (e.clientY - r.top) / r.height * worldH;
            setCam((c) => ({ ...c, x: vpRect.width / 2 - wx * c.z, y: vpRect.height / 2 - wy * c.z }));
          }}>
          <rect x={0} y={0} width={worldW} height={worldH} className="mm-bg" />
          {screened.map((L) => (
            <rect key={L.c.key} x={L.x} y={L.y} width={L.w} height={H}
              className={L.c.key === heroKey ? 'mm-hero' : 'mm-seat'} />
          ))}
          {(() => {
            const vx = Math.max(0, -cam.x / cam.z), vy = Math.max(0, -cam.y / cam.z);
            const vx2 = Math.min(worldW, (vpRect.width - cam.x) / cam.z), vy2 = Math.min(worldH, (vpRect.height - cam.y) / cam.z);
            return <rect x={vx} y={vy} width={Math.max(0, vx2 - vx)} height={Math.max(0, vy2 - vy)} className="mm-view" vectorEffect="non-scaling-stroke" />;
          })()}
        </svg>
      )}
    </div>
  );
}
