import { useEffect, useState } from 'react';

import { useCardText } from '../lib/cardText';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// PANE · LIVE — one pane as a pod, measured not estimated: the process (pid, rss,
// state, staleness), its session (uuid, transcript size), the inspector's own
// numbers from inside the process, and the API's usage per turn (input,
// cache_read, cache_creation, output) — the figure /context only approximates.
type PaneRow = { id: string; cmd: string; title?: string; canonical_name?: string; claude_uuid?: string; state?: string;
  screen_same_for_s?: number; pid?: number; rss_mb?: number; context_tokens?: number; context_at?: string; inspector?: string };
type Turn = { ts: string; model?: string; input_tokens: number; cache_read_input_tokens: number; cache_creation_input_tokens: number;
  output_tokens: number; thinking_tokens?: number; context_tokens: number };
type Usage = { pane: string; session: string; jsonl: string; jsonl_bytes: number; pid: number; rss_mb: number; inspector: string; state: string; turns: Turn[] };

const k = (n?: number) => n == null ? '·' : n >= 1000 ? `${(n / 1000).toFixed(n >= 100000 ? 0 : 1)}k` : String(n);
const hhmm = (ts?: string) => ts ? ts.slice(11, 19) : '·';
const ago = (ts?: string) => { if (!ts) return ''; const s = (Date.now() - Date.parse(ts)) / 1000; return s < 90 ? 'now' : s < 5400 ? `${Math.round(s/60)}m ago` : s < 172800 ? `${Math.round(s/3600)}h ago` : `${Math.round(s/86400)}d ago`; };

export function PaneLive({ initial = '', cardKey }: { initial?: string; cardKey?: string }) {
  const [panes, setPanes] = useState<PaneRow[]>([]);
  const [pane, setPane] = useState(initial);
  // pick a live inspected pane on first load if none chosen (fixes: default %24 is gone after rebirth → looked empty)
  const [usage, setUsage] = useState<Usage | null>(null);
  const [insp, setInsp] = useState<Record<string, unknown> | null>(null);
  const [inspErr, setInspErr] = useState('');
  const [expr, setExpr] = useState('');

  useEffect(() => {
    let dead = false;
    const tick = async () => {
      try {
        const j = await (await fetch(`${BASE}/panes`)).json();
        if (dead) return;
        const rows: PaneRow[] = (j.panes || []).filter((p: PaneRow) => /claude|node/.test(p.cmd || ''));
        setPanes(rows);
        if (!rows.some((p) => p.id === pane) && rows.length) setPane(rows.find((p) => p.inspector)?.id || rows[0].id);
      } catch { /* collector away */ }
    };
    tick(); const t = setInterval(tick, 5000);
    return () => { dead = true; clearInterval(t); };
  }, [pane]);

  useEffect(() => {
    let dead = false;
    const tick = async () => {
      try {
        const j = await (await fetch(`${BASE}/panes/usage?pane=${encodeURIComponent(pane)}&n=40`)).json();
        if (!dead) setUsage(j);
      } catch { /* keep last */ }
    };
    tick(); const t = setInterval(tick, 5000);
    return () => { dead = true; clearInterval(t); };
  }, [pane]);

  useEffect(() => {
    let dead = false;
    const tick = async () => {
      const row = panes.find((p) => p.id === pane);
      if (!row?.inspector) { setInsp(null); setInspErr(''); return; }
      try {
        const q = expr.trim() ? `&expr=${encodeURIComponent(expr)}` : '';
        const j = await (await fetch(`${BASE}/panes/inspect?pane=${encodeURIComponent(pane)}${q}`)).json();
        if (dead) return;
        if (j.error) { setInspErr(String(j.error)); setInsp(null); }
        else { setInspErr(''); setInsp(j.result?.result?.value ?? j.result); }
      } catch (e) { if (!dead) setInspErr(String(e)); }
    };
    tick(); const t = setInterval(tick, 10000);
    return () => { dead = true; clearInterval(t); };
  }, [pane, panes, expr]);

  const row = panes.find((p) => p.id === pane);
  const turns = usage?.turns || [];
  const last = turns[turns.length - 1];
  const max = Math.max(1, ...turns.map((t) => t.context_tokens));
  useCardText(cardKey, [`${pane} ${row?.state || '·'} same ${row?.screen_same_for_s ?? '·'}s pid ${row?.pid ?? '·'}`, `session · jsonl`, 'measured context', last ? `${k(last.context_tokens)} input cache_read cache_creation output` : 'no assistant turn recorded yet in this session file', '\n\n', 'inside the process', insp ? JSON.stringify(insp, null, 1) : '', inspErr, row?.inspector ? 'expr' : '', 't ctx in read new out think', ...turns.slice(-12).map((t) => `${hhmm(t.ts)} ${k(t.context_tokens)}`)]);

  return (
    <div className="pane-live">
      <div className="pl-row pl-head">
        <select value={pane} onChange={(e) => setPane(e.target.value)} title="which pane">
          {panes.map((p) => <option key={p.id} value={p.id}>{p.id} {p.canonical_name || ''} {p.inspector ? '· 🔬' : ''}</option>)}
          {!panes.some((p) => p.id === pane) && <option value={pane}>{pane}</option>}
        </select>
        <span className={`pl-state pl-${row?.state || 'unknown'}`}>{row?.state || '·'}</span>
        <span title="how long this exact screen has been up">same {row?.screen_same_for_s ?? '·'}s</span>
        <span title="process">pid {row?.pid ?? '·'} · rss {row?.rss_mb ?? '·'}MB</span>
      </div>
      <div className="pl-row pl-id" title={usage?.jsonl || ''}>
        <span>session {usage?.session ? usage.session.slice(0, 8) : '·'}</span>
        <span>jsonl {usage ? `${(usage.jsonl_bytes / 1e6).toFixed(usage.jsonl_bytes > 1e7 ? 0 : 2)}MB` : '·'}</span>
        <span>{row?.claude_uuid && usage?.session && row.claude_uuid !== usage.session ? '⚠ argv≠session' : ''}</span>
      </div>
      <div className="pl-block">
        <div className="pl-title">measured context · last turn {hhmm(last?.ts)} <span className="pl-ago">{ago(last?.ts)}</span></div>
        {last ? (
          <div className="pl-usage">
            <b title="input + cache_read + cache_creation, as billed">{k(last.context_tokens)}</b>
            <span>input {k(last.input_tokens)}</span>
            <span>cache_read {k(last.cache_read_input_tokens)}</span>
            <span>cache_creation {k(last.cache_creation_input_tokens)}</span>
            <span>output {k(last.output_tokens)}{last.thinking_tokens ? ` (think ${k(last.thinking_tokens)})` : ''}</span>
          </div>
        ) : <div className="empty">no assistant turn recorded yet in this session file</div>}
        <div className="pl-bars" title="context_tokens per turn, oldest → newest">
          {turns.map((t, i) => (
            <div key={i} className={`pl-bar${t.cache_creation_input_tokens > t.cache_read_input_tokens ? ' hot' : ''}`}
              style={{ height: `${Math.max(2, Math.round((t.context_tokens / max) * 40))}px` }}
              title={`${hhmm(t.ts)} ctx ${k(t.context_tokens)} · in ${k(t.input_tokens)} · read ${k(t.cache_read_input_tokens)} · new ${k(t.cache_creation_input_tokens)} · out ${k(t.output_tokens)}`} />
          ))}
        </div>
      </div>
      <div className="pl-block">
        <div className="pl-title">inside the process · {row?.inspector ? row.inspector : 'no inspector (launch with BUN_INSPECT=ws://127.0.0.1:<port>/dbg)'}</div>
        {insp && <pre className="pl-pre">{JSON.stringify(insp, null, 1)}</pre>}
        {inspErr && <div className="pl-err">{inspErr}</div>}
        {row?.inspector && (
          <input className="pl-expr" value={expr} placeholder="expression to evaluate inside (default: pid/rss/heap/uptime)"
            onChange={(e) => setExpr(e.target.value)} />
        )}
      </div>
      <table className="pl-table">
        <thead><tr><th>t</th><th>ctx</th><th>in</th><th>read</th><th>new</th><th>out</th><th title="thinking tokens billed this turn — the text itself is redacted by the API (signature only)">think</th></tr></thead>
        <tbody>
          {turns.slice(-12).reverse().map((t, i) => (
            <tr key={i}><td>{hhmm(t.ts)}</td><td><b>{k(t.context_tokens)}</b></td><td>{k(t.input_tokens)}</td><td>{k(t.cache_read_input_tokens)}</td><td>{k(t.cache_creation_input_tokens)}</td><td>{k(t.output_tokens)}</td><td>{t.thinking_tokens ? k(t.thinking_tokens) : '·'}</td></tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
