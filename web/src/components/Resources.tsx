import { useEffect, useRef, useState } from 'react';
import { procinfo, type ProcInfo } from '../lib/api';

// The witness watching its own diet: per-tab memory + CPU across every tab on
// the shared Firefox, including 8's own cockpit tab (★). Polls /procinfo ~4s,
// pauses when the cockpit tab is hidden (the same aperture-control the witness
// applies to all its consumption).
export function Resources({ session }: { session: string | null }) {
  const [pi, setPi] = useState<ProcInfo | null>(null);
  const [miss, setMiss] = useState(false);
  // last cumulative cpu_ms per process, to derive a live CPU% over the poll gap
  const prev = useRef<Record<string, { c: number; t: number }>>({});

  useEffect(() => {
    const sid = session || 'fox';
    let alive = true;
    let id: number | undefined;
    const rate = (key: string, cpu_ms: number, now: number): number | null => {
      const pr = prev.current[key];
      prev.current[key] = { c: cpu_ms, t: now };
      if (!pr) return null;
      const dt = now - pr.t;
      if (dt <= 0) return null;
      return Math.max(0, ((cpu_ms - pr.c) / dt) * 100); // CPU-ms over wall-ms = %
    };
    const tick = async () => {
      const p = await procinfo(sid);
      if (!alive) return;
      if (!p) { setMiss(true); return; }
      const now = Date.now();
      p.tabs.forEach((t) => { t.cpu_pct = rate('p' + t.pid, t.cpu_ms, now); });
      if (p.gpu) p.gpu.cpu_pct = rate('gpu', p.gpu.cpu_ms, now);
      setPi(p); setMiss(false);
    };
    const start = () => { if (id == null) { tick(); id = window.setInterval(tick, 4000); } };
    const stop = () => { if (id != null) { clearInterval(id); id = undefined; } };
    const onVis = () => { (document.hidden ? stop : start)(); };
    document.addEventListener('visibilitychange', onVis);
    if (!document.hidden) start();
    return () => { alive = false; stop(); document.removeEventListener('visibilitychange', onVis); };
  }, [session]);

  const self = location.host; // 8's own cockpit tab

  return (
    <section className="panel resources">
      <div className="res-body">
        {!pi && (
          <div className="empty">
            {miss ? 'procinfo unavailable — collector needs -gecko + Firefox -remote-allow-system-access' : 'reading…'}
          </div>
        )}
        {pi && (
          <table className="metrics res-tab">
            <thead><tr><th>tab</th><th>mem</th><th>cpu %</th><th>pid</th></tr></thead>
            <tbody>
              {pi.tabs.map((t, i) => {
                const mine = String(t.url).includes(self);
                return (
                  <tr key={i} className={mine ? 'self' : ''} title={t.url}>
                    <td className="tname">
                      {mine ? '★ ' : ''}{t.title || t.url}
                      {!t.exact ? <span className="shared"> shared ×{t.coresident_tabs}</span> : ''}
                    </td>
                    <td className="num">{t.mem_mb} MB</td>
                    <td className="num">{t.cpu_pct == null ? '…' : t.cpu_pct.toFixed(0) + '%'}</td>
                    <td className="num dim">{t.pid}</td>
                  </tr>
                );
              })}
              {pi.gpu && (
                <tr className="gpu" title="one GPU process, shared across all tabs — Firefox has no per-tab GPU">
                  <td className="tname">GPU · shared (all tabs)</td>
                  <td className="num">{pi.gpu.mem_mb} MB</td>
                  <td className="num">{pi.gpu.cpu_pct == null ? '…' : pi.gpu.cpu_pct.toFixed(0) + '%'}</td>
                  <td className="num dim">{pi.gpu.pid}</td>
                </tr>
              )}
              <tr className="parent">
                <td className="tname">browser core</td>
                <td className="num">{pi.parent_mem_mb} MB</td>
                <td className="num">—</td>
                <td className="num dim">—</td>
              </tr>
            </tbody>
          </table>
        )}
        {pi && <div className="res-note">★ = 8's own tab. Exact per-tab unless flagged shared; GPU is one shared process.</div>}
      </div>
    </section>
  );
}
