import { useEffect } from 'react';

// useDrag — make an element draggable by a child handle (.drag-handle),
// persisting its position per key in localStorage. Pointer-based, viewport-
// clamped, so a panel can't be dragged off-screen. The fix for #panels-overlap:
// floating panels were position:fixed and stacked; now the operator moves them.
export function useDrag(ref: React.RefObject<HTMLElement | null>, key: string, def: { x: number; y: number }) {
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let pos = def;
    try { const s = localStorage.getItem('drag:' + key); if (s) pos = JSON.parse(s); } catch { /* private mode */ }
    const clamp = (p: { x: number; y: number }) => ({
      x: Math.max(0, Math.min(p.x, window.innerWidth - 80)),
      y: Math.max(0, Math.min(p.y, window.innerHeight - 40)),
    });
    const apply = (p: { x: number; y: number }) => {
      const c = clamp(p);
      el.style.left = c.x + 'px';
      el.style.top = c.y + 'px';
      el.style.right = 'auto';
      el.style.bottom = 'auto';
    };
    apply(pos);
    const handle = el.querySelector<HTMLElement>('.drag-handle') || el;
    handle.style.cursor = 'grab';
    let start: { x: number; y: number; px: number; py: number } | null = null;
    const down = (e: PointerEvent) => {
      if ((e.target as HTMLElement).closest('button,input,textarea,select,a')) return;
      start = { x: pos.x, y: pos.y, px: e.clientX, py: e.clientY };
      handle.style.cursor = 'grabbing';
      handle.setPointerCapture(e.pointerId);
      document.body.style.userSelect = 'none';
      e.preventDefault();
    };
    const move = (e: PointerEvent) => {
      if (!start) return;
      pos = { x: start.x + (e.clientX - start.px), y: start.y + (e.clientY - start.py) };
      apply(pos);
    };
    const up = (e: PointerEvent) => {
      if (!start) return;
      start = null;
      handle.style.cursor = 'grab';
      document.body.style.userSelect = '';
      try { handle.releasePointerCapture(e.pointerId); } catch { /* */ }
      try { localStorage.setItem('drag:' + key, JSON.stringify(clamp(pos))); } catch { /* */ }
    };
    handle.addEventListener('pointerdown', down);
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', up);
    return () => {
      handle.removeEventListener('pointerdown', down);
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', up);
    };
  }, [ref, key, def.x, def.y]);
}
