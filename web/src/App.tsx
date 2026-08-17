import { useEffect, useRef, useState } from 'react';
import { PasteCurl } from './components/PasteCurl';
import { Inspector } from './components/Inspector';
import { Viewport } from './components/Viewport';
import { SessionRail } from './components/SessionRail';
import { Interaction } from './components/Interaction';
import { SessionStream } from './components/SessionStream';
import { Resources } from './components/Resources';
import { PaneCockpit } from './components/PaneCockpit';
import { Bench } from './components/Bench';
import { Splitter, SideStack, useLocal } from './components/Dock';
import { ThemePicker } from './components/ThemePicker';
import { Canvas } from './components/Canvas';
import { Transports } from './components/Transports';
import { initTheme } from './lib/theme';
import { openFeed } from './lib/feed';
import { listSessions, replay } from './lib/api';
import type { CaptureRow, Session } from './types';

export default function App() {
  const [rows, setRows] = useState<CaptureRow[]>([]);
  const [live, setLive] = useState(false);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selId, setSelId] = useState<number | null>(null);
  const [filters, setFilters] = useState({ call: true, channel: true });
  const [selSession, setSelSession] = useState<Session | null>(null);
  const [showLab, setShowLab] = useState(false);
  const [showWire, setShowWire] = useState(false);
  const [q, setQ] = useState('');
  const [replayed, setReplayed] = useState<Record<number, string>>({});
  const [themeBg] = useState(() => initTheme());
  const [showCanvas, setShowCanvas] = useLocal<boolean>('showCanvas', false);
  const [railW, setRailW] = useLocal<number>('railW', 210);
  const [sideW, setSideW] = useLocal<number>('sideW', 340);
  const clampRail = (w: number) => Math.max(150, Math.min(window.innerWidth * 0.5, w));
  const clampSide = (w: number) => Math.max(260, Math.min(window.innerWidth * 0.72, w));
  const buf = useRef<CaptureRow[]>([]);
  // per-session buffers (500/session) so View 2 survives global-ring eviction —
  // a noisy session must not evict a quiet one's history.
  const sessBuf = useRef<Map<string, CaptureRow[]>>(new Map());
  const [, setSessTick] = useState(0);
  // CROSS-VIEW TRAVEL — the four views are radio buttons (one visible at a time),
  // so context must TRAVEL when you switch: any component fires an `8:jump`
  // CustomEvent ({view, q?, focusKey?}) and App switches the view carrying the
  // context — a feed row jumps to its tab's card on canvas, WIRE jumps to LAB's
  // evidence, LAB jumps back to fire on WIRE. Views stop being dead ends.
  const [canvasFocus, setCanvasFocus] = useState('');
  useEffect(() => {
    const onJump = (e: Event) => {
      const d = (e as CustomEvent).detail || {};
      setShowCanvas(d.view === 'canvas');
      setShowLab(d.view === 'lab');
      setShowWire(d.view === 'wire');
      if (d.q !== undefined) setQ(d.q);
      if (d.focusKey) setCanvasFocus(d.focusKey);
    };
    window.addEventListener('8:jump', onJump);
    return () => window.removeEventListener('8:jump', onJump);
  }, [setShowCanvas]);

  useEffect(() => {
    const load = () => listSessions().then(setSessions).catch(() => {});
    load();
    const poll = window.setInterval(load, 3000);
    const close = openFeed(
      // bounded deque: keep the newest 200 rows, evict the oldest (FIFO).
      (row) => {
        buf.current = [...buf.current, row].slice(-200);
        setRows(buf.current);
        const k = row.session || 'wire';
        sessBuf.current.set(k, [...(sessBuf.current.get(k) || []), row].slice(-500));
        setSessTick((t) => t + 1);
      },
      setLive,
    );
    return () => { clearInterval(poll); close(); };
  }, []);

  const selected = rows.find((r) => r.id === selId) || null;
  const ql = q.trim().toLowerCase();
  const shown = rows.filter((r) => filters[r.physics] &&
    (!ql || `${r.method} ${r.detail} ${r.session} ${r.physics}`.toLowerCase().includes(ql)));
  // ATTENTION-FIRST feed: the witness's own eye-movements (viewport/canvas capture
  // ops) repeat by the hundreds and evict the real events from the 200-ring —
  // observed 188/200 rows = captureScreenshot (2026-07-27). Coalesce CONSECUTIVE
  // same-shape rows into one "×N" row (newest kept, clickable as ever); the raw
  // stream stays one toggle away. Signal first, repetition summarized.
  const [coalesce, setCoalesce] = useState(true);
  type Grouped = { row: CaptureRow; n: number };
  const grouped: Grouped[] = [];
  for (const r of shown) {
    const last = grouped[grouped.length - 1];
    if (coalesce && last && last.row.method === r.method && last.row.detail === r.detail && last.row.session === r.session) {
      last.row = r; last.n++; // keep the NEWEST of the run
    } else grouped.push({ row: r, n: 1 });
  }
  const sessionRows = selSession ? sessBuf.current.get(selSession.id) || [] : [];
  const toggle = (k: 'call' | 'channel') => setFilters((f) => ({ ...f, [k]: !f[k] }));
  const doReplay = async (id: number) => {
    setReplayed((p) => ({ ...p, [id]: '…' }));
    const res = await replay(id);
    setReplayed((p) => ({ ...p, [id]: res.error ? '✗' : `${res.status} · ${Math.round(res.latency_us || 0)}µs` }));
  };

  return (
    <div className="app">
      <main className="cols">
        {showCanvas ? <Canvas session={sessions.find((s) => s.physics === 'channel')?.id || null} focusKey={canvasFocus} /> : showLab ? <Bench /> : showWire ? <Transports /> : (<>
        <div className="rail-wrap" style={{ width: railW, flex: 'none' }}>
          <SessionRail sessions={sessions} rows={rows} filters={filters} onToggle={toggle} onSelect={setSelSession} selectedId={selSession?.id} />
        </div>
        <Splitter dir="v" onDelta={(dx) => setRailW((w) => clampRail(w + dx))} onDouble={() => setRailW(210)} />

        {selSession ? (
          <>
            <Interaction session={selSession} onClose={() => setSelSession(null)} />
            <SessionStream session={selSession} rows={sessionRows} />
          </>
        ) : (
          <>
        <section className="panel stream">
          <div className="stream-filter">
            <input className="filt-in" placeholder="filter — method, route, session, physics…" value={q} onChange={(e) => setQ(e.target.value)} />
            {q && <button className="filt-x" onClick={() => setQ('')}>clear</button>}
            <button className={`filt-x${coalesce ? ' on' : ''}`} title="summarize repeated identical ops (the witness's own captures) into one ×N row" onClick={() => setCoalesce((v) => !v)}>{coalesce ? '≡ coalesced' : '≣ raw'}</button>
            <span className="filt-cnt">{coalesce ? `${grouped.length} of ` : ''}{shown.length}/{rows.length}</span>
          </div>
          <div className="cap-head">
            <span className="ln">ln</span>
            <span className="phys">phys</span>
            <span className="method">method</span>
            <span className="detail">route</span>
            <span className="t">time</span>
            <span className="sess">sess</span>
            <span className="rp">replay</span>
          </div>
          {shown.length === 0 && (
            <div className="empty">no traffic{q ? ' matches the filter' : ' yet — drive a session or Fire a curl'}.</div>
          )}
          <ul className="rows">
            {grouped.slice().reverse().map(({ row: r, n }, i) => (
              <li
                key={r.id}
                className={`cap-row phys-${r.physics}${r.id === selId ? ' sel' : ''}`}
                onClick={() => setSelId(r.id)}
              >
                <span className="ln">{grouped.length - i}</span>
                <span className="phys">{r.physics}</span>
                <span className="method">{r.method}{n > 1 ? <b className="xn"> ×{n}</b> : null}</span>
                <span className="detail">{r.detail}</span>
                <span className="t">{new Date(r.at).toLocaleTimeString()}</span>
                <span className="sess">{r.session}</span>
                <span className="rp">
                  {r.ledgerId != null
                    ? <button className="replay" title="replay this request" onClick={(e) => { e.stopPropagation(); doReplay(r.ledgerId!); }}>{replayed[r.ledgerId] || '▶ replay'}</button>
                    : <span className="no">·</span>}
                  {r.tab && <button className="replay see" title="see this tab's card on the canvas" onClick={(e) => { e.stopPropagation(); window.dispatchEvent(new CustomEvent('8:jump', { detail: { view: 'canvas', focusKey: r.session + r.tab } })); }}>◉ see</button>}
                </span>
              </li>
            ))}
          </ul>
        </section>
        <Splitter dir="v" onDelta={(dx) => setSideW((w) => clampSide(w - dx))} onDouble={() => setSideW(340)} />
        <div className="side" style={{ width: sideW, minWidth: 0, flex: 'none' }}>
          <SideStack panes={[
            { id: 'resources', title: 'resources', node: <Resources session={sessions.find((s) => s.physics === 'channel')?.id || null} /> },
            { id: 'viewport', title: 'viewport', node: <Viewport session={sessions.find((s) => s.physics === 'channel')?.id || null} /> },
            { id: 'inspector', title: 'inspector', node: <Inspector row={selected} /> },
            { id: 'curl', title: 'compose', node: <PasteCurl /> },
            { id: 'panes', title: 'panes · send', node: <PaneCockpit /> },
          ]} />
        </div>
          </>
        )}
        </>)}
      </main>

      <header className="statusline">
        <span className="mode">NORMAL</span>
        <span className="brand">8</span>
        <span className={live ? 'live' : 'dead'}>{live ? '● LIVE' : '○ OFFLINE'}</span>
        <span>SESSIONS {sessions.length ? sessions.map((s) => s.id).join(', ') : '—'}</span>
        <span>CAPTURE {rows.length}</span>
        {/* MUTUALLY EXCLUSIVE views (fix, 2026-08-11 — audit found these were
            independent booleans + a priority chain, so clicking LAB while CANVAS
            was on didn't switch). Selecting a view now deselects the others;
            clicking the active one again returns to the feed. */}
        <span className="canvas-toggle" onClick={() => setShowCanvas((v) => { if (!v) { setShowLab(false); setShowWire(false); } return !v; })}>{showCanvas ? '▣ CANVAS' : '▢ CANVAS'}</span>
        <span className="lab-toggle" onClick={() => setShowLab((v) => { if (!v) { setShowCanvas(false); setShowWire(false); } return !v; })}>{showLab ? '▣ LAB' : '▢ LAB'}</span>
        <span className="wire-toggle" onClick={() => setShowWire((v) => { if (!v) { setShowCanvas(false); setShowLab(false); } return !v; })}>{showWire ? '▣ WIRE' : '▢ WIRE'}</span>
        <span className="keys">click row → inspect · paste a curl → Fire</span>
      </header>
      <ThemePicker initial={themeBg} />
    </div>
  );
}
