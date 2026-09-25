import { useEffect, useSyncExternalStore } from 'react';

// cardText — the LAYOUT REPORT store, one per card key, two regimes (#1147):
//   text     a body REPORTS the lines it renders; the engine MEASURES them with
//            pretext (arithmetic, no DOM) — for feeds and text-like bodies.
//   measured a STRUCTURED body (host/profile/panes/budget — rows, buttons,
//            bars) is measured ONCE in the DOM by a ResizeObserver on its
//            wrapper (CardFrame) and the height lands here. No parallel
//            plaintext copy of a structured layout — that copy drifted.
// Picture cards (aspect) never report. A tiny external store: report from a
// body, subscribe from the canvas. Reports are coalesced per animation frame
// so a polling body that re-renders every few seconds costs one layout pass.
const texts = new Map<string, string>();
const heights = new Map<string, number>();
const subs = new Set<() => void>();
let ver = 0;
let raf = 0;
export function bumpLayout() {
  if (raf) return;
  // outside a browser (unit tests, SSR) there is no rAF — coalesce synchronously
  // so the module loads and runs without a DOM.
  if (typeof requestAnimationFrame !== 'function') { ver++; subs.forEach((s) => s()); return; }
  raf = requestAnimationFrame(() => { raf = 0; ver++; subs.forEach((s) => s()); });
}
const bump = bumpLayout;
export function reportText(key: string, text: string) {
  if (texts.get(key) === text) return;
  texts.set(key, text);
  bump();
}
export function textOf(key: string): string | undefined { return texts.get(key); }
/** dom regime: the measured body height (world px) of a structured card */
export function reportHeight(key: string, h: number) {
  const r = Math.round(h);
  if (r <= 0 || heights.get(key) === r) return;
  heights.set(key, r);
  bump();
}
export function heightOf(key: string): number | undefined { return heights.get(key); }
export function useReportedTexts(): number {
  return useSyncExternalStore((cb) => { subs.add(cb); return () => { subs.delete(cb); }; }, () => ver, () => ver);
}
/** in a body: declare the lines this body shows (the engine sizes the card from them) */
export function useCardText(key: string | undefined, lines: (string | number | null | undefined)[]) {
  const text = lines.filter((l) => l !== null && l !== undefined && l !== '').join('\n');
  useEffect(() => { if (key) reportText(key, text); }, [key, text]);
}
