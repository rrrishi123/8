// Parse a session's /source (Appium/WebDriver XML hierarchy) into a flat list
// of elements with bounds + attributes + depth — the single input both the
// screen overlay and the element tree render from. Physics-general: the same
// shape will come from a BiDi DOM walk later (getBoundingClientRect → bounds).

export interface SrcElement {
  tag: string;
  bounds?: { x: number; y: number; w: number; h: number };
  attrs: Record<string, string>;
  depth: number;
  index: number;
}

// The collector returns {"value":"<hierarchy>…</hierarchy>"} for call sessions.
export function parseSource(value: string): SrcElement[] {
  const out: SrcElement[] = [];
  let doc: Document;
  try {
    doc = new DOMParser().parseFromString(value, 'application/xml');
  } catch {
    return out;
  }
  let i = 0;
  const walk = (el: Element, depth: number) => {
    const attrs: Record<string, string> = {};
    for (const a of Array.from(el.attributes)) attrs[a.name] = a.value;
    let bounds: SrcElement['bounds'];
    const b = attrs.bounds; // Appium native: "[x1,y1][x2,y2]"
    const m = b && b.match(/\[(\d+),(\d+)\]\[(\d+),(\d+)\]/);
    if (m) {
      const x1 = +m[1], y1 = +m[2], x2 = +m[3], y2 = +m[4];
      bounds = { x: x1, y: y1, w: x2 - x1, h: y2 - y1 };
    }
    out.push({ tag: el.nodeName, bounds, attrs, depth, index: i++ });
    for (const c of Array.from(el.children)) walk(c, depth + 1);
  };
  if (doc.documentElement) walk(doc.documentElement, 0);
  return out;
}

// The locators Appium Inspector surfaces, derived from an element's attributes.
export function locators(e: SrcElement): { by: string; value: string }[] {
  const a = e.attrs;
  const out: { by: string; value: string }[] = [];
  if (a['resource-id'] || a.id) out.push({ by: 'id', value: a['resource-id'] || a.id });
  if (a['content-desc'] || a['accessibility-id']) out.push({ by: 'accessibility id', value: a['content-desc'] || a['accessibility-id'] });
  if (a['class'] || a.tag) out.push({ by: 'class', value: a['class'] || a.tag });
  if (a.text) out.push({ by: 'text', value: a.text });
  return out;
}

// A readable label for a tree node / row.
export function elementLabel(e: SrcElement): string {
  const a = e.attrs;
  const id = a['resource-id'] ? a['resource-id'].split('/').pop() : '';
  const desc = a['content-desc'] || a.text || '';
  const cls = (a['class'] || e.tag).split('.').pop();
  return [cls, id && `#${id}`, desc && `"${desc.slice(0, 24)}"`].filter(Boolean).join(' ');
}
