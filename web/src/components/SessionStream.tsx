import type { CaptureRow, Session } from '../types';

// View 2's right pane: the calls + channel frames for ONE session — including
// 8's own pokes (COLLECTOR origin), not just the browser/device side. Fed from a
// per-session buffer in App that survives global-ring eviction (a noisy session
// must not evict a quiet one's history). Unified timeline, origin glyph, error
// rows flagged — per the peer's review.
export function SessionStream({ session, rows }: { session: Session; rows: CaptureRow[] }) {
  return (
    <section className="panel sess-stream">
      <div className="panel-h">
        traffic · {session.id.slice(0, 8)} · {session.physics}
        <span className="ss-count">{rows.length}</span>
      </div>
      <div className="cap-head">
        <span className="ln">ln</span>
        <span className="org">src</span>
        <span className="method">method</span>
        <span className="detail">detail</span>
      </div>
      {rows.length === 0 && (
        <div className="empty">no traffic yet — drive it. 8's own /source, /act, /run show here too.</div>
      )}
      <ul className="rows">
        {rows.slice().reverse().map((r, i) => {
          const status = r.raw && typeof (r.raw as any).status === 'number' ? (r.raw as any).status : undefined;
          const err = status !== undefined && status >= 400;
          // context==null channel events are session-global, not this tab's — flag them.
          const global = r.origin === 'BIDI' && session.physics === 'channel' && !r.tab;
          return (
            <li key={r.id} className={`cap-row phys-${r.physics}${err ? ' err-row' : ''}`}>
              <span className="ln">{rows.length - i}</span>
              <span className="org" title={r.origin === 'COLLECTOR' ? "8's own call" : 'the wire'}>
                {r.origin === 'COLLECTOR' ? '8' : '∿'}
              </span>
              <span className="method">{r.method}</span>
              <span className="detail">{global ? '[GLOBAL] ' : ''}{r.detail}</span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
