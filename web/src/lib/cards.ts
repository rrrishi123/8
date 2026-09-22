import { prepare, layout, type PreparedText } from '@chenglou/pretext';
import type { ReactNode } from 'react';
import { heightOf, bumpLayout } from './cardText';

// ── THE CARD GRAMMAR ──────────────────────────────────────────────────────────
// ONE grammar for every card on the cockpit canvas: a tab, a tmux pane, a host,
// a profile, the budget HUD, the tasks list, the heart (pane·live), the panes
// cockpit, the wire feed, recording, resources, compose, clock, matrix, inner
// host, portal. A card is {identity, lane, kind, title, meta, size-source, body}.
// Its SIZE comes from one of THREE regimes (#1147):
//   • aspect  — a picture card (viewport): h = w / aspect (+ header)
//   • text    — a text card: h = pretext.layout(text, w) (+ chrome), NO reflow
//   • measure — a STRUCTURED card (host/profile/panes/budget): its body is
//               measured ONCE in the DOM (ResizeObserver in CardFrame) and the
//               height lands in the same layout store the text regime reads.
//               Until the first measurement it falls back to its text (if any)
//               or the 3-line floor. No parallel plaintext copy to drift.
// The engine below turns cards into rects (masonry per lane, lanes side by side)
// and answers "is this rect on screen" for virtualization/occlusion.
export type CardKind =
  | 'tab' | 'seat' | 'host' | 'profile'
  | 'budget' | 'tasks' | 'panes' | 'heart' | 'wire'
  | 'record' | 'resources' | 'compose' | 'clock' | 'matrix' | 'inner' | 'portal';

export interface Card {
  key: string;          // stable identity (React key, pin key, seq key)
  lane: string;         // the group it belongs to (host / profile / type)
  kind: CardKind;
  title: string;
  meta?: string;        // second line under the title
  aspect?: number;      // picture cards: width / height of the body
  text?: string;        // text cards: the body text pretext measures ('\n' = hard break)
  measure?: 'dom';      // structured cards: body height measured once in the DOM (see cardText.reportHeight)
  span?: number;        // unit columns (default 1)
  minH?: number;        // body floor (px, world)
  maxH?: number;        // body ceiling (px, world) — beyond it the body scrolls
  hero?: boolean;       // the pinned card
  bare?: boolean;       // body brings its own header (viewport cards)
  node: ReactNode;      // the body
}

export interface Lane {
  key: string;
  label: string;
  kind: 'browser' | 'seat' | 'host' | 'profile' | 'type';
  cols?: number;        // unit columns wide (default derived from card count)
  stacked?: boolean;    // solitaire deck: cards overlap, the hero on top
  badge?: string;       // e.g. "12 tabs"
  tone?: 'chrome' | 'firefox' | 'seat' | 'type';
  cards: Card[];
  actions?: ReactNode;  // lane-head controls (fan/stack, + tab …)
}

// ── GRID CONSTANTS (world px). The unit is the ONE size every card is a multiple
// of; everything else derives from it. Tune here, nowhere else.
export const GRID = {
  unit: 560,     // one column, world px
  gap: 24,       // between cards
  laneGap: 96,   // between lanes
  headH: 34,     // card header (kind · title · meta · actions)
  padY: 10,      // body vertical padding (text cards)
  padX: 12,
  border: 1,     // .card border width — box-sizing:border-box eats 2×this from the interior, so the card's total height must include it or every dom card scrolls 2px (#1147)
  laneHead: 44,  // lane head strip above a lane
  stackOX: 32, stackOY: 36, // solitaire offsets
  x0: 120, y0: 175,          // world origin
};

// FLUID UNIT — responsiveness regime A: the column width tracks the viewport so
// TEXT cards reflow (pretext re-wraps at the new width) instead of a fixed 560
// world column that is only camera-zoomed. Clamped so text stays legible. DOM
// flow panels get responsiveness from CSS container queries — the two-regime
// standard. Canvas calls setUnit(viewportWidth) on resize; a change re-lays-out.
let UNIT = GRID.unit;
export const unitOf = () => UNIT;
export function setUnit(viewportW: number): number {
  if (!viewportW) return UNIT;
  const u = Math.round(Math.max(420, Math.min(760, viewportW * 0.46)));
  if (u !== UNIT) { UNIT = u; bumpLayout(); }
  return UNIT;
}

// ── MEASURE: pretext, cached ──────────────────────────────────────────────────
// prepare() once per (text, font) — the expensive segmentation+measurement pass;
// layout() per (width) — arithmetic. The cache is bounded so a chatty feed can't
// grow it forever. Font/line-height come from CSS custom properties so the
// measurement and the rendering share ONE source of truth (--card-font/--card-lh).
let FONT = '12px ui-monospace, Menlo, monospace';
let LH = 17;
export function syncFontFromCSS() {
  try {
    const cs = getComputedStyle(document.documentElement);
    const f = cs.getPropertyValue('--card-font').trim();
    const lh = parseFloat(cs.getPropertyValue('--card-lh'));
    if (f) FONT = f;
    if (lh > 0) LH = lh;
  } catch { /* SSR / no DOM: keep defaults */ }
  return { font: FONT, lineHeight: LH };
}
export const cardFont = () => ({ font: FONT, lineHeight: LH });

const cache = new Map<string, PreparedText>();
const CACHE_MAX = 3000;
// a late webfont (JetBrains Mono) changes every glyph advance: prepared texts
// measured against the fallback face are wrong once it lands. Drop the cache
// and re-layout when the document's fonts finish loading. Measured (dom)
// cards re-report on their own — the ResizeObserver sees the reflow.
try {
  document.fonts?.addEventListener?.('loadingdone', () => { cache.clear(); syncFontFromCSS(); bumpLayout(); });
} catch { /* no DOM */ }
function prepared(text: string): PreparedText {
  const k = FONT + '\u0000' + text;
  let p = cache.get(k);
  if (!p) {
    p = prepare(text, FONT, { whiteSpace: 'pre-wrap' });
    if (cache.size >= CACHE_MAX) { const first = cache.keys().next().value; if (first !== undefined) cache.delete(first); }
    cache.set(k, p);
  }
  return p;
}
/** height (world px) of `text` wrapped at `width` — pure arithmetic after the first call for this text */
export function measureText(text: string, width: number): { height: number; lineCount: number } {
  if (!text) return { height: 0, lineCount: 0 };
  const r = layout(prepared(text), Math.max(40, width), LH);
  return { height: r.height, lineCount: r.lineCount };
}

// ── SIZE: a card's rect from its size-source ─────────────────────────────────
export function cardSize(c: Card): { w: number; h: number } {
  const span = Math.max(1, c.span || 1);
  const w = span * UNIT + (span - 1) * GRID.gap;
  const head = c.bare ? 0 : GRID.headH;
  let body: number;
  const measured = c.measure === 'dom' ? heightOf(c.key) : undefined;
  if (c.aspect) body = Math.round(w / Math.max(0.2, c.aspect)) + (c.bare ? GRID.headH : 0);
  else if (measured !== undefined) body = measured; // dom regime: the wrapper's own height (its padding included)
  else {
    const m = measureText(c.text || '', w - 2 * GRID.padX);
    body = Math.round(m.height) + 2 * GRID.padY;
  }
  const minH = c.minH ?? (c.aspect ? 0 : LH * 3 + 2 * GRID.padY);
  const maxH = c.maxH ?? (c.aspect ? Infinity : Math.round(UNIT * 1.15));
  body = Math.max(minH, Math.min(maxH, body));
  // + the card's own top+bottom border: rect.h is a border-box (box-sizing:border-box),
  // so the interior the body gets is rect.h − 2×border. Without this the measured body
  // is 2px taller than its slot and every structured card shows a permanent 2px scroll.
  return { w, h: head + body + 2 * GRID.border };
}

// ── PACK: masonry per lane ───────────────────────────────────────────────────
// Cards are placed IN ORDER into the lowest-topped run of `span` adjacent columns
// (ties → leftmost). Placement is a fold over the ordered list, so a card's rect
// depends only on the cards BEFORE it: appending a card never moves an existing
// one — the fix for "a new card takes the bottom card's place". Stacked lanes are
// the solitaire deck (overlapping, hero on top).
export interface Placed { key: string; x: number; y: number; w: number; h: number; z: number; top: boolean; card: Card }
export interface PlacedLane { lane: Lane; x: number; y: number; w: number; h: number; cards: Placed[] }

// COLUMN COUNT IS FIXED PER LANE KIND — never derived from the card count. A
// count-derived width (√n) re-packs the whole lane every time n crosses a
// boundary, which is exactly "a new card moved the bottom card". With a fixed
// width the masonry fold is append-only: a newcomer's rect depends only on the
// cards before it. `Lane.cols` overrides per lane; span never exceeds cols.
export const LANE_COLS: Record<Lane['kind'], number> = { browser: 8, seat: 8, host: 4, profile: 2, type: 2 };
export function laneCols(l: Lane): number {
  const maxSpan = l.cards.reduce((a, c) => Math.max(a, c.span || 1), 1);
  const want = l.cols && l.cols > 0 ? l.cols : (LANE_COLS[l.kind] ?? 2);
  return Math.max(1, maxSpan, want);
}

export function packLane(l: Lane, heroKey: string): { w: number; h: number; cards: Placed[] } {
  const out: Placed[] = [];
  if (l.stacked && l.cards.length > 0) {
    const topKey = l.cards.some((c) => c.key === heroKey) ? heroKey : l.cards[0].key;
    const others = l.cards.filter((c) => c.key !== topKey);
    const s0 = cardSize(l.cards.find((c) => c.key === topKey)!);
    let maxW = 0, maxH = 0;
    l.cards.forEach((c) => {
      const isTop = c.key === topKey;
      const oi = isTop ? 0 : others.indexOf(c) + 1;
      const z = isTop ? others.length + 2 : others.length - others.indexOf(c);
      const p: Placed = { key: c.key, x: GRID.stackOX * oi, y: GRID.stackOY * oi, w: s0.w, h: s0.h, z, top: isTop, card: c };
      out.push(p);
      maxW = Math.max(maxW, p.x + p.w); maxH = Math.max(maxH, p.y + p.h);
    });
    return { w: maxW, h: maxH, cards: out };
  }
  const cols = laneCols(l);
  const tops = new Array<number>(cols).fill(0);
  const colX = (i: number) => i * (UNIT + GRID.gap);
  let maxW = 0;
  for (const c of l.cards) {
    const { w, h } = cardSize(c);
    const span = Math.min(cols, Math.max(1, c.span || 1));
    let best = 0, bestTop = Infinity;
    for (let i = 0; i + span <= cols; i++) {
      const t = Math.max(...tops.slice(i, i + span));
      if (t < bestTop) { bestTop = t; best = i; }
    }
    const x = colX(best), y = bestTop;
    out.push({ key: c.key, x, y, w, h, z: c.hero ? 5 : 1, top: true, card: c });
    for (let i = best; i < best + span; i++) tops[i] = y + h + GRID.gap;
    maxW = Math.max(maxW, x + w);
  }
  const h = Math.max(0, Math.max(...tops, 0) - GRID.gap);
  return { w: Math.max(maxW, cols * UNIT + (cols - 1) * GRID.gap), h, cards: out };
}

/** lanes side by side, left → right, in the given order; each lane may carry an operator offset */
export function packWorld(lanes: Lane[], heroKey: string, offsets: Record<string, { x: number; y: number }>): { lanes: PlacedLane[]; w: number; h: number } {
  const placed: PlacedLane[] = [];
  let x = GRID.x0, maxH = 0;
  for (const l of lanes) {
    const p = packLane(l, heroKey);
    const off = offsets[l.key] || { x: 0, y: 0 };
    const lx = x + off.x, ly = GRID.y0 + off.y;
    placed.push({ lane: l, x: lx, y: ly, w: p.w, h: p.h, cards: p.cards.map((c) => ({ ...c, x: lx + c.x, y: ly + GRID.laneHead + c.y })) });
    x += p.w + GRID.laneGap;
    maxH = Math.max(maxH, ly + GRID.laneHead + p.h);
  }
  return { lanes: placed, w: Math.max(x, GRID.x0 * 2), h: maxH + GRID.y0 };
}

// ── OCCLUSION ────────────────────────────────────────────────────────────────
export interface Cam { x: number; y: number; z: number }
/** does the world rect land inside the viewport (+margin, screen px)? */
export function onScreen(r: { x: number; y: number; w: number; h: number }, cam: Cam, vp: { w: number; h: number }, margin = 120): boolean {
  if (!vp.w || !vp.h) return true;
  const sx = cam.x + r.x * cam.z, sy = cam.y + r.y * cam.z, sw = r.w * cam.z, sh = r.h * cam.z;
  return sx + sw > -margin && sx < vp.w + margin && sy + sh > -margin && sy < vp.h + margin;
}

// ── ORDER LEDGER: first-seen sequence per key, persisted. The masonry fold walks
// cards in this order, so arrival order IS layout order and a newcomer can only
// land AFTER everything already placed.
export class SeqLedger {
  private seq: Record<string, number>;
  private next: number;
  constructor(private storeKey = '8dock:cardSeq') {
    let s: Record<string, number> = {};
    try { s = JSON.parse(localStorage.getItem(storeKey) || '{}'); } catch { /* */ }
    this.seq = s;
    this.next = Object.values(s).reduce((a, b) => Math.max(a, b), 0) + 1;
  }
  of(key: string): number {
    if (this.seq[key] == null) { this.seq[key] = this.next++; this.save(); }
    return this.seq[key];
  }
  sort<T extends { key: string }>(xs: T[]): T[] { return [...xs].sort((a, b) => this.of(a.key) - this.of(b.key)); }
  reset() { this.seq = {}; this.next = 1; this.save(); }
  private save() { try { localStorage.setItem(this.storeKey, JSON.stringify(this.seq)); } catch { /* */ } }
}

// ── SEMANTIC ZOOM: which level a zoom factor means. hosts (far) → profiles → tabs (near).
export type Level = 'hosts' | 'profiles' | 'tabs';
export const LEVELS: Level[] = ['hosts', 'profiles', 'tabs'];
export const ZOOM_LEVEL = { hosts: 0.25, profiles: 0.42 }; // z < hosts → hosts; z < profiles → profiles; else tabs
export function levelForZoom(z: number): Level { return z < ZOOM_LEVEL.hosts ? 'hosts' : z < ZOOM_LEVEL.profiles ? 'profiles' : 'tabs'; }
