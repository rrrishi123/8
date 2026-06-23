import { useEffect, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

interface Tab { context: string; url: string; }

// A live-ish screen mirror of a chosen session tab. BiDi has no screencast
// stream, so we poll /shot (captureScreenshot, JPEG) ~1fps and swap the <img>.
export function Viewport({ session }: { session: string | null }) {
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [ctx, setCtx] = useState('');
  const [src, setSrc] = useState('');
  const [err, setErr] = useState(false);

  // Load the tab list once per session.
  useEffect(() => {
    if (!session) return;
    fetch(`${BASE}/tabs?session=${encodeURIComponent(session)}`)
      .then((r) => r.json())
      .then((j) => {
        const t: Tab[] = j.tabs || [];
        setTabs(t);
        setCtx((cur) => cur || (t[0] ? t[0].context : ''));
      })
      .catch(() => {});
  }, [session]);

  // Poll the chosen tab's screenshot.
  useEffect(() => {
    if (!session) return;
    const sid = session; // narrow for the async closure below
    let alive = true;
    async function shot() {
      try {
        const q = ctx ? `&context=${encodeURIComponent(ctx)}` : '';
        const r = await fetch(`${BASE}/shot?session=${encodeURIComponent(sid)}${q}`);
        const j = await r.json();
        if (alive && j.data) { setSrc(j.data); setErr(false); }
      } catch { if (alive) setErr(true); }
    }
    // Bound the screenshot churn: poll every 5s, and PAUSE entirely when the
    // cockpit tab is hidden (this churn over hours was a chunk of the GBs).
    let id: number | undefined;
    const start = () => { if (id == null) { shot(); id = window.setInterval(shot, 5000); } };
    const stop = () => { if (id != null) { clearInterval(id); id = undefined; } };
    const onVis = () => { (document.hidden ? stop : start)(); };
    document.addEventListener('visibilitychange', onVis);
    if (!document.hidden) start();
    return () => { alive = false; stop(); document.removeEventListener('visibilitychange', onVis); };
  }, [session, ctx]);

  const label = (t: Tab) => {
    try { return new URL(t.url).host || t.context.slice(0, 8); }
    catch { return t.url || t.context.slice(0, 8); }
  };

  return (
    <section className="panel viewport">
      <div className="panel-h">
        viewport — live screen {err ? '· ✗' : src ? '· ●' : ''}
        {tabs.length > 0 && (
          <select className="tab-pick" value={ctx} onChange={(e) => setCtx(e.target.value)}>
            {tabs.map((t) => (
              <option key={t.context} value={t.context}>{label(t)}</option>
            ))}
          </select>
        )}
      </div>
      {src
        ? <img className="vp-img" src={src} alt="session viewport" />
        : <div className="empty">{session ? 'waiting for a frame…' : 'no session'}</div>}
    </section>
  );
}
