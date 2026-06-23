import { useEffect, useState } from 'react';
import { shot } from '../lib/api';
import type { CaptureRow } from '../types';

// Click a capture row -> see its full payload, AND the session's screen at the
// top (the image precedes the data — like Appium Inspector on attach). The
// screen comes through the wire's native screenshot (device for call sessions,
// tab for channel).
export function Inspector({ row }: { row: CaptureRow | null }) {
  const [img, setImg] = useState('');
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    setImg('');
    const t = row?.shot;
    if (!t) return;
    let alive = true;
    setLoading(true);
    shot(t.session, t.context)
      .then((d) => { if (alive) setImg(d); })
      .catch(() => {})
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [row?.id]);

  return (
    <section className="panel inspector">
      <div className="panel-h">inspector</div>
      {!row && <div className="empty">select a row to inspect it.</div>}
      {row && (
        <div className="insp">
          <div className="insp-head">
            <span className={`tag phys-${row.physics}`}>{row.physics}</span>
            <span className="tag">{row.origin}</span>
            <span className="insp-method">{row.method}</span>
            <span className="insp-sess">{row.session}</span>
            {row.tab && <span className="insp-tab">tab {row.tab.slice(0, 8)}</span>}
          </div>
          {row.shot && (
            img
              ? <img className="insp-shot" src={img} alt="session screen" />
              : <div className="insp-shot-wait">{loading ? 'capturing screen through the wire…' : 'no screen'}</div>
          )}
          <div className="insp-detail">{row.detail}</div>
          <pre className="insp-raw">{pretty(row.raw)}</pre>
        </div>
      )}
    </section>
  );
}

function pretty(x: unknown): string {
  try { return JSON.stringify(x, null, 2); } catch { return String(x); }
}
