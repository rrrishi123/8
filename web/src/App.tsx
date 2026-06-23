import { useEffect, useRef, useState } from 'react';
import { PasteCurl } from './components/PasteCurl';
import { Inspector } from './components/Inspector';
import { Viewport } from './components/Viewport';
import { SessionRail } from './components/SessionRail';
import { Interaction } from './components/Interaction';
import { SessionStream } from './components/SessionStream';
import { openFeed } from './lib/feed';
import { listSessions } from './lib/api';
import type { CaptureRow, Session } from './types';

export default function App() {
  const [rows, setRows] = useState<CaptureRow[]>([]);
  const [live, setLive] = useState(false);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selId, setSelId] = useState<number | null>(null);
  const [filters, setFilters] = useState({ call: true, channel: true });
  const [selSession, setSelSession] = useState<Session | null>(null);
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
  const shown = rows.filter((r) => filters[r.physics]);
  const sessionRows = selSession ? sessBuf.current.get(selSession.id) || [] : [];
  const toggle = (k: 'call' | 'channel') => setFilters((f) => ({ ...f, [k]: !f[k] }));

  return (
    <div className="app">
      <main className="cols">
        <SessionRail sessions={sessions} rows={rows} filters={filters} onToggle={toggle} onSelect={setSelSession} />

        {selSession ? (
          <>
            <Interaction session={selSession} onClose={() => setSelSession(null)} />
            <SessionStream session={selSession} rows={sessionRows} />
          </>
        ) : (
          <>
        <section className="panel stream">
          <div className="cap-head">
            <span className="ln">ln</span>
            <span className="phys">phys</span>
            <span className="method">method</span>
            <span className="detail">route</span>
            <span className="sess">sess</span>
          </div>
          {shown.length === 0 && (
            <div className="empty">no traffic yet — drive a session or Fire a curl. nothing synthetic.</div>
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
                <span className="sess">{r.session}</span>
              </li>
            ))}
          </ul>
        </section>
        <div className="side">
          <Viewport session={sessions.find((s) => s.physics === 'channel')?.id || null} />
          <Inspector row={selected} />
          <PasteCurl />
        </div>
          </>
        )}
      </main>

      <header className="statusline">
        <span className="mode">NORMAL</span>
        <span className="brand">8</span>
        <span className={live ? 'live' : 'dead'}>{live ? '● LIVE' : '○ OFFLINE'}</span>
        <span>SESSIONS {sessions.length ? sessions.map((s) => s.id).join(', ') : '—'}</span>
        <span>CAPTURE {rows.length}</span>
        <span className="keys">click row → inspect · paste a curl → Fire</span>
      </header>
    </div>
  );
}
