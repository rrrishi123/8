import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Viewport } from './Viewport';
import { Resources } from './Resources';
import { PasteCurl } from './PasteCurl';
import { useLocal } from './Dock';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// pretext spatial cockpit: every SEAT gets a live viewport, laid out in a world
// you pan/zoom. Firefox + every real device, side by side; bird's-eye = see them
// all at once. Perspectives are saved cameras. Pan = drag; zoom = wheel.
interface Seat { id: string; physics: string; hub: string; status: string }
interface PaneRect { id: string; x: number; y: number; w: number; h: number; title: string; node: ReactNode; gravity?: boolean }

export function Canvas({ session }: { session: string | null }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [cam, setCam] = useLocal<{ x: number; y: number; z: number }>('cam', { x: 60, y: 30, z: 0.42 });
  const [seats, setSeats] = useState<Seat[]>([]);

  useEffect(() => {
    const load = () => fetch(`${BASE}/sessions`).then((r) => r.json())
      .then((j) => setSeats(((j.sessions as Seat[]) || []).filter((s) => s.status !== 'disconnected'))).catch(() => {});
    load();
    const t = window.setInterval(() => { if (!document.hidden) load(); }, 5000);
    return () => clearInterval(t);
  }, []);

  const label = (s: Seat) => s.physics === 'channel' ? 'firefox · channel'
    : s.hub?.includes('4444') ? 'firefox · request'
    : s.hub?.includes('4723') ? 'device · appium' : 'seat';

  // one viewport per seat, left→right; resources + compose on the row below
  const seatPanes: PaneRect[] = seats.map((s, i) => ({
    id: 'seat-' + s.id, title: `${label(s)} · ${s.id.slice(0, 8)}`,
    x: 120 + i * 620, y: 200, w: 560, h: 900, gravity: i === 0,
    node: <Viewport session={s.id} title={`${label(s)} · ${s.id.slice(0, 8)}`} />,
  }));
  const yBelow = 1180;
  const panes: PaneRect[] = [
    ...seatPanes,
    { id: 'resources', title: 'resources · per-tab mem/cpu', x: 120, y: yBelow, w: 620, h: 430, node: <Resources session={session} /> },
    { id: 'compose', title: 'compose · record → replay', x: 780, y: yBelow, w: 620, h: 430, node: <PasteCurl /> },
  ];
  const worldW = Math.max(2400, 120 + Math.max(seats.length, 2) * 620);

  const drag = useRef<{ x: number; y: number } | null>(null);
  const onDown = (e: React.PointerEvent) => { if ((e.target as HTMLElement).closest('.cpane-body, .persp-bar')) return; drag.current = { x: e.clientX, y: e.clientY }; };
  const onMove = (e: React.PointerEvent) => { if (!drag.current) return; const dx = e.clientX - drag.current.x, dy = e.clientY - drag.current.y; drag.current = { x: e.clientX, y: e.clientY }; setCam((c) => ({ ...c, x: c.x + dx, y: c.y + dy })); };
  const onUp = () => { drag.current = null; };
  const onWheel = (e: React.WheelEvent) => {
    const r = wrap.current!.getBoundingClientRect(); const mx = e.clientX - r.left, my = e.clientY - r.top;
    setCam((c) => { const nz = Math.max(0.18, Math.min(2.5, c.z * (e.deltaY < 0 ? 1.12 : 0.89))); const k = nz / c.z; return { z: nz, x: mx - (mx - c.x) * k, y: my - (my - c.y) * k }; });
  };
  const goto = (rx: number, ry: number, rw: number, rh: number, z: number) => { const r = wrap.current!.getBoundingClientRect(); setCam({ z, x: r.width / 2 - (rx + rw / 2) * z, y: r.height / 2 - (ry + rh / 2) * z }); };
  const persp = {
    p1: () => goto(120, 200, 560, 900, 0.9),                 // one seat (the gravity browser)
    p2: () => goto(120, 200, Math.min(seats.length, 3) * 620, 900, 0.55), // the seats row
    bird: () => goto(0, 120, worldW, 1560, 0.36),             // everything: all seats + panes
  };

  return (
    <div className="canvas-wrap" ref={wrap} onPointerDown={onDown} onPointerMove={onMove} onPointerUp={onUp} onWheel={onWheel}>
      <div className="world" style={{ transform: `translate(${cam.x}px,${cam.y}px) scale(${cam.z})` }}>
        {/* each element IS its own box (pretext) — the component's own panel,
            positioned freely in the world; no wrapper box around it. */}
        {panes.map((p) => (
          <div key={p.id} className={`cbox${p.gravity ? ' gravity' : ''}`} style={{ left: p.x, top: p.y, width: p.w, height: p.h }}>
            {p.node}
          </div>
        ))}
      </div>
      <div className="persp-bar">
        <button onClick={persp.p1} title="one seat">P1 · act</button>
        <button onClick={persp.p2} title="all seats, side by side">P2 · seats</button>
        <button onClick={persp.bird} title="see everything">◇ bird's-eye</button>
        <span className="persp-z">{seats.length} seats · {Math.round(cam.z * 100)}%</span>
      </div>
    </div>
  );
}
