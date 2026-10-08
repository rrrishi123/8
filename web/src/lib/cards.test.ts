import { describe, it, expect, beforeEach } from 'vitest';
import { GRID, cardSize, setUnit, unitOf, setMeasurer, type Card } from './cards';
import { reportHeight } from './cardText';

// The card LAYOUT ENGINE, tested as pure arithmetic. pretext measures through a
// canvas context and can't run in Node, so we inject a deterministic monospace
// stub (~7px/char, wraps at the given width) and check cardSize()/setUnit() with
// no DOM at all. These pin the regime boundary — text reflows with width, dom is
// measured — which has drifted three times (heart, matrix, the 2px border).
const CHAR_W = 7, STUB_LH = 17;
beforeEach(() => {
  setMeasurer((text, width) => {
    if (!text) return { height: 0, lineCount: 0 };
    const perLine = Math.max(1, Math.floor(width / CHAR_W));
    const lineCount = Math.max(1, Math.ceil(text.length / perLine));
    return { height: lineCount * STUB_LH, lineCount };
  });
  setUnit(2000); // land UNIT on its 760 ceiling, a known start
});

const textCard = (text: string): Card => ({ key: 't', lane: 'l', kind: 'wire', title: 'x', text, node: null });
const domCard = (): Card => ({ key: 'd', lane: 'l', kind: 'panes', title: 'x', measure: 'dom', node: null });

describe('setUnit — the fluid unit (regime A)', () => {
  it('is a no-op at viewportW = 0', () => {
    const before = unitOf();
    expect(setUnit(0)).toBe(before);
    expect(unitOf()).toBe(before);
  });
  it('clamps to [420, 760]', () => {
    expect(setUnit(100)).toBe(420);   // 46  -> floor
    expect(setUnit(9000)).toBe(760);  // 4140 -> ceiling
    expect(setUnit(1000)).toBe(460);  // 460  in-band, tracks the viewport
  });
});

describe('cardSize — text regime reflows with width', () => {
  it('a long paragraph is shorter at UNIT 760 than at 420', () => {
    const para = 'lorem ipsum '.repeat(90); // wraps at both widths, clamps at neither
    setUnit(9000); const wide = cardSize(textCard(para)).h;   // UNIT 760
    setUnit(100);  const narrow = cardSize(textCard(para)).h; // UNIT 420
    expect(wide).toBeLessThan(narrow);
  });
});

describe('cardSize — dom regime', () => {
  it('h === head + body + 2*border for a measured card (would have caught the 2px clip)', () => {
    reportHeight('d', 200);
    setUnit(1000);
    expect(cardSize(domCard()).h).toBe(GRID.headH + 200 + 2 * GRID.border);
  });
  it('height is width-INdependent when a measured height exists (the regime boundary)', () => {
    reportHeight('d', 200);
    setUnit(9000); const wide = cardSize(domCard()).h;
    setUnit(100);  const narrow = cardSize(domCard()).h;
    expect(wide).toBe(narrow);
  });
});
