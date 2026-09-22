import { useEffect, useSyncExternalStore } from 'react';

// cardText — components REPORT the text they render; the layout engine MEASURES
// it (pretext) and sizes the card. No component ever measures itself in the DOM.
// A tiny external store: report(key, text) from inside a body, subscribe from
// the canvas. Reports are coalesced per animation frame so a polling body that
// re-renders every few seconds costs one layout pass, not one per setState.
const texts = new Map<string, string>();
const subs = new Set<() => void>();
let ver = 0;
let raf = 0;
function bump() {
  if (raf) return;
  raf = requestAnimationFrame(() => { raf = 0; ver++; subs.forEach((s) => s()); });
}
export function reportText(key: string, text: string) {
  if (texts.get(key) === text) return;
  texts.set(key, text);
  bump();
}
export function textOf(key: string): string | undefined { return texts.get(key); }
export function useReportedTexts(): number {
  return useSyncExternalStore((cb) => { subs.add(cb); return () => { subs.delete(cb); }; }, () => ver, () => ver);
}
/** in a body: declare the lines this body shows (the engine sizes the card from them) */
export function useCardText(key: string | undefined, lines: (string | number | null | undefined)[]) {
  const text = lines.filter((l) => l !== null && l !== undefined && l !== '').join('\n');
  useEffect(() => { if (key) reportText(key, text); }, [key, text]);
}
