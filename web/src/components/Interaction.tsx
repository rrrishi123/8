import { useEffect, useRef, useState } from 'react';
import { shot } from '../lib/api';
import { parseSource, locators, elementLabel, type SrcElement } from '../lib/source';
import type { Session } from '../types';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// The interaction surface — Appium-Inspector-on-attach, physics-general:
// device/page SCREEN (/shot) with element-bounds OVERLAY (/source -> parser),
// an element TREE, and a SELECTED-ELEMENT panel with locators + Tap (/act).
export function Interaction({ session, onClose }: { session: Session; onClose: () => void }) {
  const [img, setImg] = useState('');
  const [els, setEls] = useState<SrcElement[]>([]);
  const [sel, setSel] = useState<SrcElement | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState('');
  const [collapsed, setCollapsed] = useState<Set<number>>(new Set());

  async function refresh() {
    setBusy(true); setNote('');
    try {
      const [s, srcResp] = await Promise.all([
        shot(session.id).catch(() => ''),
        fetch(`${BASE}/source?session=${encodeURIComponent(session.id)}`).then((r) => r.ok ? r.json() : null).catch(() => null),
      ]);
      if (s) setImg(s);
      if (srcResp && srcResp.value) setEls(parseSource(srcResp.value));
      else setNote('source not available for this physics yet (BiDi DOM pending)');
    } finally { setBusy(false); }
  }
  useEffect(() => { setSel(null); setCollapsed(new Set()); refresh(); /* eslint-disable-next-line */ }, [session.id]);

  const root = els.find((e) => e.bounds);
  const devW = root?.bounds ? root.bounds.x + root.bounds.w : 1080;
  const devH = root?.bounds ? root.bounds.y + root.bounds.h : 2400;

  // Collapse: hide any element nested under a collapsed ancestor (flat list +
  // depth -> a node hides everything deeper that follows it until depth drops back).
  const visibleEls: SrcElement[] = [];
  let hideBelow = Infinity;
  for (const e of els) {
    if (e.depth > hideBelow) continue;
    hideBelow = Infinity;
    visibleEls.push(e);
    if (collapsed.has(e.index)) hideBelow = e.depth;
  }

  // Live stream when the session exposes one (MJPEG renders live in <img>);
  // otherwise the polled /shot frame.
  const streamUrl = session.stream ? `${BASE}/stream?session=${encodeURIComponent(session.id)}` : '';
  const screenSrc = streamUrl || img;

  async function act(body: Record<string, unknown>) {
    await fetch(`${BASE}/act?session=${encodeURIComponent(session.id)}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }).catch(() => {});
    if (!streamUrl) setTimeout(refresh, 700); // poll mode re-shots; a live stream already shows it
  }
  function tap(e: SrcElement) {
    if (e.bounds) act({ action: 'tap', x: e.bounds.x + Math.floor(e.bounds.w / 2), y: e.bounds.y + Math.floor(e.bounds.h / 2) });
  }

  // Free interaction on the screen itself: click = tap, drag = swipe — mapped
  // from the rendered frame back to device coordinates ("any coordinate, live").
  const drag = useRef<{ x: number; y: number } | null>(null);
  function devXY(e: React.MouseEvent<HTMLElement>) {
    const r = e.currentTarget.getBoundingClientRect();
    return { x: Math.round(((e.clientX - r.left) / r.width) * devW), y: Math.round(((e.clientY - r.top) / r.height) * devH) };
  }
  function onDown(e: React.MouseEvent<HTMLElement>) { drag.current = devXY(e); }
  function onUp(e: React.MouseEvent<HTMLElement>) {
    const s = drag.current; drag.current = null;
    if (!s) return;
    const p = devXY(e);
    if (Math.hypot(p.x - s.x, p.y - s.y) > 24) act({ action: 'swipe', x: s.x, y: s.y, x2: p.x, y2: p.y, ms: 350 });
    else act({ action: 'tap', x: p.x, y: p.y });
  }

  return (
    <section className="panel interaction-view">
      <div className="panel-h">
        interaction · {session.id} · {session.kind}·{session.physics}
        <button className="ix-btn" onClick={refresh} disabled={busy}>{busy ? '…' : '↻'}</button>
        <button className="ix-btn ix-close" onClick={onClose}>✕ stream</button>
      </div>
      <div className="ix-cols">
        <div className="ix-screen">
          {screenSrc ? (
            <div className={`ix-screen-wrap${root?.bounds ? '' : ' natural'}`} style={root?.bounds ? { aspectRatio: `${devW} / ${devH}` } : undefined}
              onMouseDown={onDown} onMouseUp={onUp} title="click = tap · drag = swipe">
              {streamUrl && <span className="ix-live">● LIVE</span>}
              <img src={screenSrc} alt="device" draggable={false} />
              {/* overlay is visual only (pointer-events:none) — the screen is a free tap/drag canvas */}
              <svg className="ix-overlay" viewBox={`0 0 ${devW} ${devH}`} preserveAspectRatio="none">
                {els.filter((e) => e.bounds).map((e) => (
                  <rect key={e.index} x={e.bounds!.x} y={e.bounds!.y} width={e.bounds!.w} height={e.bounds!.h}
                    className={sel?.index === e.index ? 'sel' : ''} />
                ))}
              </svg>
            </div>
          ) : <div className="empty">{busy ? 'loading screen…' : 'no screen'}</div>}
        </div>

        <div className="ix-tree">
          <div className="ix-sub">source · {els.length} {note && <span className="ix-note">{note}</span>}</div>
          <ul>
            {visibleEls.map((e) => {
              const hasKids = !!els[e.index + 1] && els[e.index + 1].depth > e.depth;
              const isCol = collapsed.has(e.index);
              return (
                <li key={e.index} className={`ix-node${sel?.index === e.index ? ' sel' : ''}`}
                  style={{ paddingLeft: 6 + e.depth * 11 }} onClick={() => setSel(e)} onDoubleClick={() => tap(e)}>
                  {hasKids ? (
                    <span className="ix-caret" onClick={(ev) => { ev.stopPropagation(); setCollapsed((c) => { const n = new Set(c); if (n.has(e.index)) n.delete(e.index); else n.add(e.index); return n; }); }}>
                      {isCol ? '▸' : '▾'}
                    </span>
                  ) : <span className="ix-caret ix-leaf">·</span>}
                  {elementLabel(e)}
                </li>
              );
            })}
          </ul>
        </div>

        <div className="ix-sel">
          <div className="ix-sub">selected element</div>
          {!sel && <div className="empty">click a node in the tree to inspect · the screen is a live tap/swipe canvas</div>}
          {sel && (
            <div className="ix-detail">
              {locators(sel).map((l, i) => (
                <div key={i} className="ix-loc-row"><span className="by">{l.by}</span><span className="val">{l.value}</span></div>
              ))}
              {sel.bounds && <div className="ix-loc-row"><span className="by">bounds</span><span className="val">{sel.bounds.x},{sel.bounds.y} {sel.bounds.w}×{sel.bounds.h}</span></div>}
              {sel.bounds && <button className="fire" onClick={() => tap(sel)}>▷ Tap</button>}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
