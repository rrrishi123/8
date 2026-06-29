import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';

// ── persisted layout state ───────────────────────────────────────────────────
export function useLocal<T>(key: string, init: T): [T, (v: T | ((p: T) => T)) => void] {
  const [v, setV] = useState<T>(() => {
    try { const s = localStorage.getItem('8dock:' + key); return s ? (JSON.parse(s) as T) : init; }
    catch { return init; }
  });
  useEffect(() => { try { localStorage.setItem('8dock:' + key, JSON.stringify(v)); } catch { /* ignore */ } }, [key, v]);
  return [v, setV];
}

// ── an evident drag-to-resize bar (vertical = resizes width, horizontal = height)
export function Splitter({ dir, onDelta, onDouble }: { dir: 'v' | 'h'; onDelta: (px: number) => void; onDouble?: () => void }) {
  const drag = useRef<number | null>(null);
  const down = (e: React.PointerEvent) => {
    e.preventDefault();
    drag.current = dir === 'v' ? e.clientX : e.clientY;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  };
  const move = (e: React.PointerEvent) => {
    if (drag.current == null) return;
    const cur = dir === 'v' ? e.clientX : e.clientY;
    onDelta(cur - drag.current);
    drag.current = cur;
  };
  const up = (e: React.PointerEvent) => { drag.current = null; try { (e.target as HTMLElement).releasePointerCapture(e.pointerId); } catch { /* */ } };
  return (
    <div
      className={`splitter splitter-${dir}`}
      onPointerDown={down}
      onPointerMove={move}
      onPointerUp={up}
      onDoubleClick={onDouble}
      title="drag to resize · double-click to reset"
    ><span className="grip" /></div>
  );
}

export interface PaneDef { id: string; title: string; node: ReactNode; }

// ── a vertical stack of panes: each resizable (h-splitters), collapsible,
// pinnable, and reorderable by dragging its header handle. ───────────────────
export function SideStack({ panes }: { panes: PaneDef[] }) {
  const ids = panes.map((p) => p.id);
  const [order, setOrder] = useLocal<string[]>('order', ids);
  const [heights, setHeights] = useLocal<Record<string, number>>('heights', {});
  const [collapsed, setCollapsed] = useLocal<string[]>('collapsed', []);
  const [pinned, setPinned] = useLocal<string[]>('pinned', []);
  const [solo, setSolo] = useLocal<string>('solo', '');
  const [dragId, setDragId] = useState<string | null>(null);
  const stackRef = useRef<HTMLDivElement>(null);

  // keep order in sync if the set of panes changes
  useEffect(() => {
    setOrder((o) => {
      const kept = o.filter((id) => ids.includes(id));
      const added = ids.filter((id) => !kept.includes(id));
      return [...kept, ...added];
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ids.join(',')]);

  // pinned float to the top, preserving relative order within each group
  const sorted = [...order].sort((a, b) => (pinned.includes(b) ? 1 : 0) - (pinned.includes(a) ? 1 : 0));
  const byId = Object.fromEntries(panes.map((p) => [p.id, p]));

  const toggle = (set: string[], setSet: (v: string[] | ((p: string[]) => string[])) => void, id: string) =>
    setSet((s) => (s.includes(id) ? s.filter((x) => x !== id) : [...s, id]));

  const onHeaderDown = (id: string) => (e: React.PointerEvent) => {
    if ((e.target as HTMLElement).closest('.pane-btn')) return; // let buttons click
    setDragId(id);
  };
  const onMove = useCallback((e: React.PointerEvent) => {
    if (!dragId || !stackRef.current) return;
    const panesEls = [...stackRef.current.querySelectorAll('[data-pane]')] as HTMLElement[];
    const y = e.clientY;
    let target = sorted[sorted.length - 1];
    for (const el of panesEls) {
      const r = el.getBoundingClientRect();
      if (y < r.top + r.height / 2) { target = el.dataset.pane!; break; }
    }
    if (target && target !== dragId) {
      setOrder((o) => {
        const a = o.filter((x) => x !== dragId);
        const i = a.indexOf(target);
        a.splice(i < 0 ? a.length : i, 0, dragId);
        return a;
      });
    }
  }, [dragId, sorted, setOrder]);
  const onUp = () => setDragId(null);

  const resize = (id: string, dy: number) =>
    setHeights((h) => ({ ...h, [id]: Math.max(64, (h[id] ?? 220) + dy) }));

  return (
    <div className="sidestack" ref={stackRef} onPointerMove={onMove} onPointerUp={onUp}>
      {sorted.map((id, idx) => {
        const p = byId[id]; if (!p) return null;
        const isPin = pinned.includes(id);
        const isSolo = solo === id;
        const soloing = !!solo;
        const isCol = collapsed.includes(id) || (soloing && !isSolo); // soloed-out → header only
        const last = idx === sorted.length - 1;
        const fill = isSolo || (last && !isCol && !soloing);
        const h = (isCol || fill) ? undefined : (heights[id] ?? 220);
        return (
          <div key={id} data-pane={id} className={`dpane${isCol ? ' collapsed' : ''}${dragId === id ? ' dragging' : ''}${fill ? ' flex' : ''}`}
            style={h != null ? { height: h, flex: 'none' } : undefined}>
            <div className="pane-h" onPointerDown={onHeaderDown(id)} title="drag to move">
              <span className="pane-grip">⠿</span>
              <span className="pane-title">{p.title}</span>
              <button className="pane-btn" title={isSolo ? 'restore' : 'maximize'} onClick={() => setSolo(isSolo ? '' : id)}>{isSolo ? '⤡' : '⤢'}</button>
              <button className={`pane-btn${isPin ? ' on' : ''}`} title={isPin ? 'unpin' : 'pin to top'} onClick={() => toggle(pinned, setPinned, id)}>{isPin ? '📌' : '📍'}</button>
              <button className="pane-btn" title={isCol ? 'expand' : 'collapse'} onClick={() => toggle(collapsed, setCollapsed, id)}>{isCol ? '▸' : '▾'}</button>
            </div>
            {!isCol && <div className="pane-body">{p.node}</div>}
            {!last && !isCol && !soloing && <Splitter dir="h" onDelta={(dy) => resize(id, dy)} onDouble={() => setHeights((s) => { const n = { ...s }; delete n[id]; return n; })} />}
          </div>
        );
      })}
    </div>
  );
}
