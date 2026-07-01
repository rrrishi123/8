import { useEffect, useRef, useState } from 'react';
import { PasteCurl } from './components/PasteCurl';
import { Inspector } from './components/Inspector';
import { Viewport } from './components/Viewport';
import { SessionRail } from './components/SessionRail';
import { Interaction } from './components/Interaction';
import { SessionStream } from './components/SessionStream';
import { Resources } from './components/Resources';
import { Bench } from './components/Bench';
import { Splitter, SideStack, useLocal } from './components/Dock';
import { ThemePicker } from './components/ThemePicker';
import { Canvas } from './components/Canvas';
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
        {showCanvas ? <Canvas session={sessions.find((s) => s.physics === 'channel')?.id || null} /> : showLab ? <Bench /> : (<>
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
            <span className="filt-cnt">{shown.length}/{rows.length}</span>
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
            {shown.slice().reverse().map((r, i) => (
              <li
                key={r.id}
                className={`cap-row phys-${r.physics}${r.id === selId ? ' sel' : ''}`}
                onClick={() => setSelId(r.id)}
              >
                <span className="ln">{shown.length - i}</span>
                <span className="phys">{r.physics}</span>
                <span className="method">{r.method}</span>
                <span className="detail">{r.detail}</span>
                <span className="t">{new Date(r.at).toLocaleTimeString()}</span>
                <span className="sess">{r.session}</span>
                <span className="rp">
                  {r.ledgerId != null
                    ? <button className="replay" title="replay this request" onClick={(e) => { e.stopPropagation(); doReplay(r.ledgerId!); }}>{replayed[r.ledgerId] || '▶ replay'}</button>
                    : <span className="no">·</span>}
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
        <span className="canvas-toggle" onClick={() => setShowCanvas((v) => !v)}>{showCanvas ? '▣ CANVAS' : '▢ CANVAS'}</span>
        <span className="lab-toggle" onClick={() => setShowLab((v) => !v)}>{showLab ? '▣ LAB' : '▢ LAB'}</span>
        <span className="keys">click row → inspect · paste a curl → Fire</span>
      </header>
      <ThemePicker initial={themeBg} />
    </div>
  );
}
