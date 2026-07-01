// One background color → a coherent palette. Text color is chosen by WCAG
// relative luminance (dark bg → light ink, light bg → dark ink); panels,
// borders, dims are all derived by mixing toward the ink. Accent hues
// (blue/purple/green…) are left alone — they read on any base.

const hx = (n: number) => Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, '0');
const parse = (h: string) => { const s = h.replace('#', ''); return [0, 2, 4].map((i) => parseInt(s.slice(i, i + 2), 16)); };
const mix = (a: string, b: string, t: number) => { const A = parse(a), B = parse(b); return '#' + A.map((v, i) => hx(v + (B[i] - v) * t)).join(''); };
const lum = (h: string) => {
  const c = parse(h).map((v) => { const x = v / 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); });
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
};

export function applyTheme(bg: string) {
  const dark = lum(bg) < 0.4;
  const fg = dark ? '#d8d8d8' : '#1a1a1a';
  const set = (k: string, v: string) => document.documentElement.style.setProperty(k, v);
  set('--bg', bg);
  set('--bg2', mix(bg, fg, 0.06));
  set('--panel', mix(bg, fg, 0.035));
  set('--border', mix(bg, fg, 0.18));
  set('--cursorline', mix(bg, fg, 0.08));
  set('--fg', fg);
  set('--fg-dim', mix(fg, bg, 0.40));
  set('--fg-faint', mix(fg, bg, 0.62));
  set('--gutter', mix(fg, bg, 0.55));
  set('--sel', mix(bg, '#6a9bd8', 0.32));
  set('--sel-fg', fg);
  set('--sl-bg', mix(bg, fg, 0.12));
  set('--sl-fg', fg);
  try { localStorage.setItem('8theme', bg); } catch { /* */ }
}

export function initTheme(): string {
  let bg = '#000000'; // true black by default
  try { bg = localStorage.getItem('8theme') || '#000000'; } catch { /* */ }
  applyTheme(bg);
  return bg;
}
