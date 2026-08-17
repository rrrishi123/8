import { useEffect, useState } from 'react';
import { identity, panesSend, type PaneIdentity, type PaneSendResult } from '../lib/api';

// PANE COCKPIT (#641): the operator's side of the /panes/send nerve — list the
// live claude panes (from live /identity: pane + self-declared name + title),
// pick all or any subset, one prompt, Send. The backend fans it through the
// guarded sendToPane throat and answers per-pane {pane,sent}.
export function PaneCockpit() {
  const [panes, setPanes] = useState<PaneIdentity[]>([]);
  const [sel, setSel] = useState<Record<string, boolean>>({});
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<PaneSendResult | null>(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    let dead = false;
    const load = async () => {
      try {
        const all = await identity();
        if (dead) return;
        setPanes(all.filter((p) => p.pane && (p.cmd || '').toLowerCase().includes('claude')));
      } catch { /* collector away — keep the last roster */ }
    };
    load();
    const t = setInterval(load, 5000);
    return () => { dead = true; clearInterval(t); };
  }, []);

  const chosen = panes.filter((p) => sel[p.pane!]).map((p) => p.pane!);
  const allSelected = panes.length > 0 && chosen.length === panes.length;
  const toggleAll = () => {
    const next: Record<string, boolean> = {};
    if (!allSelected) for (const p of panes) next[p.pane!] = true;
    setSel(next);
  };
  const send = async () => {
    if (!text.trim() || busy) return;
    setBusy(true); setErr(''); setRes(null);
    try {
      setRes(await panesSend(text, chosen, allSelected || chosen.length === 0));
    } catch (e) { setErr(String(e instanceof Error ? e.message : e)); }
    setBusy(false);
  };

  return (
    <div className="pane-cockpit">
      <div className="pc-list">
        <label className="pc-row pc-all">
          <input type="checkbox" checked={allSelected} onChange={toggleAll} />
          <b>all live claude panes ({panes.length})</b>
        </label>
        {panes.map((p) => (
          <label key={p.pane} className="pc-row" title={p.title || ''}>
            <input type="checkbox" checked={!!sel[p.pane!]} onChange={() => setSel((s) => ({ ...s, [p.pane!]: !s[p.pane!] }))} />
            <span className="pc-pane">{p.pane}</span>
            <span className="pc-name">{p.name || '·'}</span>
            <span className="pc-title">{(p.title || '').slice(0, 42)}</span>
          </label>
        ))}
        {panes.length === 0 && <div className="empty">no live claude panes seen yet</div>}
      </div>
      <textarea
        className="pc-text" rows={3} value={text} placeholder="one prompt → the chosen minds (empty selection = all)"
        onChange={(e) => setText(e.target.value)}
      />
      <div className="pc-actions">
        <button className="pc-send" disabled={busy || !text.trim()} onClick={send}>
          {busy ? 'sending…' : `send → ${allSelected || chosen.length === 0 ? 'ALL' : chosen.join(' ')}`}
        </button>
        {res && <span className="pc-res ok">{res.sent}/{res.total} landed{res.targets.filter((t) => !t.sent).length ? ` · failed: ${res.targets.filter((t) => !t.sent).map((t) => t.pane).join(' ')}` : ''}</span>}
        {err && <span className="pc-res err">{err}</span>}
      </div>
    </div>
  );
}
