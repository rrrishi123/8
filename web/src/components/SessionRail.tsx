import { useState } from 'react';
import type { CaptureRow, Session } from '../types';
import { Logo8 } from './Logo';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// Attach-a-session: paste any provider's hub URL (creds inline) + session id and
// the whole cockpit works on it — rail, Interaction (inspector-on-attach), shot/
// source/act, liveness. The collector probes before attaching (verdict > claim).
function AttachForm() {
  const [hub, setHub] = useState('');
  const [sid, setSid] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const attach = async () => {
    setBusy(true); setNote('');
    try {
      const r = await fetch(`${BASE}/attach`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ hub: hub.trim(), session_id: sid.trim() }),
      });
      const t = await r.text();
      setNote(r.ok ? '✓ attached — appears in the rail in ~3s' : `✗ ${t.slice(0, 90)}`);
      if (r.ok) { setHub(''); setSid(''); }
    } catch (e) { setNote('✗ ' + String(e).slice(0, 80)); }
    setBusy(false);
  };
  return (
    <details className="attach">
      <summary>+ attach a session</summary>
      <input className="attach-in" placeholder="hub url (https://user:key@hub…/wd/hub)" value={hub} onChange={(e) => setHub(e.target.value)} />
      <input className="attach-in" placeholder="session id" value={sid} onChange={(e) => setSid(e.target.value)} />
      <button className="fire attach-go" disabled={busy || !hub.trim() || !sid.trim()} onClick={attach}>{busy ? '…' : '▷ attach'}</button>
      {note && <div className="attach-note">{note}</div>}
      <div className="attach-hint">any provider's live CALL session — LambdaTest, local Appium, any WebDriver hub. channel (ws) attach: next.</div>
    </details>
  );
}

interface Props {
  sessions: Session[];
  rows: CaptureRow[];
  filters: { call: boolean; channel: boolean };
  onToggle: (k: 'call' | 'channel') => void;
  onSelect: (s: Session) => void;
  selectedId?: string;
}

// Left rail: physics filters (with live counts that actually filter the stream)
// + the live sessions. Mirrors the design's PHYSICS / SESSIONS rail.
export function SessionRail({ sessions, rows, filters, onToggle, onSelect, selectedId }: Props) {
  const callN = rows.filter((r) => r.physics === 'call').length;
  const chanN = rows.filter((r) => r.physics === 'channel').length;
  // the one Firefox channel is shared across tabs; each event is tagged with
  // its source context (tab). Distinct tabs seen so far:
  const tabs = [...new Set(rows.map((r) => r.tab).filter(Boolean))] as string[];
  return (
    <aside className="rail">
      <Logo8 />
      <ul className="filters">
        <li className={`filt${filters.call ? ' on' : ''}`} onClick={() => onToggle('call')}>
          <span className="box">[{filters.call ? 'x' : ' '}]</span>
          <span className="call">CALL</span>
          <span className="sub">http · rest</span>
          <span className="cnt">{callN}</span>
        </li>
        <li className={`filt${filters.channel ? ' on' : ''}`} onClick={() => onToggle('channel')}>
          <span className="box">[{filters.channel ? 'x' : ' '}]</span>
          <span className="chan">CHAN</span>
          <span className="sub">ws · cdp/bidi</span>
          <span className="cnt">{chanN}</span>
        </li>
      </ul>
      <div className="panel-h">sessions {sessions.length}</div>
      <ul className="sess-list">
        {sessions.length === 0 && <li className="empty">none</li>}
        {sessions.map((s) => (
          <li key={s.id} className={`sess-card${s.id === selectedId ? ' sel' : ''}${s.status === 'disconnected' ? ' dead' : ''}`} onClick={() => onSelect(s)} title="open interaction">
            <span className="dot" /> <span className="sid">{s.id}</span>
            <span className="sub">{s.kind} · {s.physics}{s.status === 'disconnected' ? ' · ✕ disconnected' : s.id === selectedId ? ' · ◂ open' : ' · click to inspect ▸'}</span>
          </li>
        ))}
      </ul>
      <AttachForm />

      <div className="panel-h">tabs on channel {tabs.length}</div>
      <ul className="sess-list">
        {tabs.length === 0 && <li className="empty">— (waiting for traffic)</li>}
        {tabs.map((t) => (
          <li key={t} className="tab-row">
            <span className="sid">{t}</span>
            <span className="cnt">{rows.filter((r) => r.tab === t).length}</span>
          </li>
        ))}
      </ul>
    </aside>
  );
}
