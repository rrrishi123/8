import { useEffect, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

interface Tab { context: string; url: string; }
type Seeing = 'pixels' | 'channel' | 'request';

// The gravity center: a REAL target seen three ways. Not the cockpit's own tab
// (no recursive self-PNG), and not one interpretation — the same target through
// pixels (/shot), channel (BiDi DOM), and request (classic source).
export function Viewport({ session, title }: { session: string | null; title?: string }) {
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [ctx, setCtx] = useState('');
  const [reqSeat, setReqSeat] = useState('');
  const [seeing, setSeeing] = useState<Seeing>('pixels');
  const [src, setSrc] = useState('');   // pixels: data-url
  const [text, setText] = useState(''); // channel DOM / request source
  const [err, setErr] = useState(false);

  // real targets only (exclude THIS cockpit tab) + resolve the request seat
  useEffect(() => {
    if (!session) return;
    fetch(`${BASE}/tabs?session=${encodeURIComponent(session)}`).then((r) => r.json()).then((j) => {
      const all: Tab[] = j.tabs || [];
      const real = all.filter((t) => !String(t.url || '').includes(location.host)); // not myself
      const list = real.length ? real : all;
      setTabs(list);
      setCtx((cur) => cur || (list[0] ? list[0].context : ''));
    }).catch(() => {});
    fetch(`${BASE}/sessions`).then((r) => r.json())
      .then((j) => setReqSeat((j.sessions || []).find((s: { physics: string; kind: string; id: string }) => s.physics === 'call' && s.kind === 'local')?.id || ''))
      .catch(() => {});
  }, [session]);

  // poll the chosen seeing of the chosen target
  useEffect(() => {
    if (!session) return; // device seats have no context — still /shot fine
    let alive = true;
    const cq = ctx ? `&context=${encodeURIComponent(ctx)}` : '';
    async function look() {
      try {
        if (seeing === 'pixels') {
          const j = await (await fetch(`${BASE}/shot?session=${encodeURIComponent(session!)}${cq}`)).json();
          if (alive && j.data) { setSrc(j.data); setErr(false); }
        } else if (seeing === 'channel') {
          const j = await (await fetch(`${BASE}/source?session=${encodeURIComponent(session!)}${cq}`)).json();
          if (alive) { const n = ((j.value || '').match(/<node/g) || []).length; setText(`channel · BiDi DOM — ${n} nodes\n\n` + String(j.value || '').replace(/></g, '>\n<').slice(0, 6000)); setErr(false); }
        } else {
          if (!reqSeat) { setText('request seat not available'); return; }
          const j = await (await fetch(`${BASE}/source?session=${encodeURIComponent(reqSeat)}`)).json();
          if (alive) { setText(`request · classic source — ${String(j.value || '').length} b\n\n` + String(j.value || '').slice(0, 6000)); setErr(false); }
        }
      } catch { if (alive) setErr(true); }
    }
    let id: number | undefined;
    const start = () => { if (id == null) { look(); id = window.setInterval(look, seeing === 'pixels' ? 4000 : 8000); } };
    const stop = () => { if (id != null) { clearInterval(id); id = undefined; } };
    const onVis = () => { (document.hidden ? stop : start)(); };
    document.addEventListener('visibilitychange', onVis);
    if (!document.hidden) start();
    return () => { alive = false; stop(); document.removeEventListener('visibilitychange', onVis); };
  }, [session, ctx, seeing, reqSeat]);

  const label = (t: Tab) => { try { return new URL(t.url).host || t.context; } catch { return t.url || t.context; } };

  return (
    <section className="panel viewport">
      <div className="panel-h">
        {title || 'viewport'} {err ? '· ✗' : '· ●'}
        <span className="seeing-tabs">
          {(['pixels', 'channel', 'request'] as Seeing[]).map((s) => (
            <button key={s} className={seeing === s ? 'on' : ''} onClick={() => setSeeing(s)} title={s === 'pixels' ? 'screenshot' : s === 'channel' ? 'BiDi DOM' : 'classic source'}>{s}</button>
          ))}
        </span>
        {tabs.length > 0 && (
          <select className="tab-pick" value={ctx} onChange={(e) => setCtx(e.target.value)}>
            {tabs.map((t) => <option key={t.context} value={t.context}>{label(t)}</option>)}
          </select>
        )}
      </div>
      {seeing === 'pixels'
        ? (src ? <img className="vp-img" src={src} alt="viewport" /> : <div className="empty">{session ? 'waiting for a frame…' : 'no session'}</div>)
        : <pre className="vp-text">{text || 'reading…'}</pre>}
    </section>
  );
}
