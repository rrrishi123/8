import { useEffect, useRef, useState } from 'react';
import { feedUrl } from '../lib/api';

type WireRow = { id: number; method: string; target: string; mode: 'CALL' | 'CHANNEL'; at: number };

let wseq = 0;

// WireLog — the visible EFFERENT strip. The broker echoes every command it injects
// to /events, the collector re-publishes them to /feed as origin:"WIRE" __cmd frames
// (so a context-less agent driving the RAW wire is visible, not only 8's own /act).
// This shows the CAUSE (the command on the wire) next to the EFFECT the operator
// already sees (the driven card rising to the top of its stack). Collapsible, so it
// never clutters the canvas. Its own EventSource — isolated from the capture feed.
export function WireLog() {
  const [rows, setRows] = useState<WireRow[]>([]);
  const [open, setOpen] = useState(true);
  useEffect(() => {
    const es = new EventSource(feedUrl);
    es.onmessage = (e) => {
      try {
        const d = JSON.parse(e.data);
        if (d.origin !== 'WIRE' || !d.frame || !d.frame.__cmd) return;
        const f = d.frame;
        const p = f.params || {};
        const ctx = p.context || (p.target && p.target.context) || '';
        const target = String(p.url || (ctx ? ctx.slice(0, 8) : d.session || 'wire'));
        const mode: 'CALL' | 'CHANNEL' = f.method === 'http_request' ? 'CALL' : 'CHANNEL';
        setRows((r) => [{ id: ++wseq, method: f.method, target, mode, at: Date.now() }, ...r].slice(0, 12));
      } catch { /* keepalive comment / non-JSON */ }
    };
    return () => es.close();
  }, []);
  if (!rows.length) return null;
  return (
    <div className={`wire-log${open ? '' : ' collapsed'}`}>
      <button className="wire-log-head" onClick={() => setOpen((o) => !o)} title="the efferent — every command on the wire">
        <span className="wire-dot" /> wire · efferent <span className="wire-count">{rows.length}</span> <span className="wire-caret">{open ? '▾' : '▸'}</span>
      </button>
      {open && (
        <div className="wire-log-body">
          {rows.map((r) => (
            <div key={r.id} className="wire-row">
              <span className={`wire-mode ${r.mode.toLowerCase()}`}>{r.mode}</span>
              <span className="wire-method">{r.method}</span>
              <span className="wire-target" title={r.target}>{r.target}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
