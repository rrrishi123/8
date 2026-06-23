import type { CaptureRow, Session } from '../types';
import { Logo8 } from './Logo';

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
