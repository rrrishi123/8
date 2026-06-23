import type { ParsedCurl } from '../types';

// Split a command line into tokens, honoring single/double quotes and escapes.
// This is the part that actually bites; the flag pass is trivial once tokens
// are correct.
function splitQuoted(input: string): string[] {
  const out: string[] = [];
  let cur = '';
  let inS = false;
  let inD = false;
  let esc = false;
  for (const ch of input) {
    if (esc) { cur += ch; esc = false; continue; }
    if (ch === '\\') { esc = true; continue; }
    if (ch === "'" && !inD) { inS = !inS; continue; }
    if (ch === '"' && !inS) { inD = !inD; continue; }
    if (ch === ' ' && !inS && !inD) { if (cur) { out.push(cur); cur = ''; } continue; }
    cur += ch;
  }
  if (cur) out.push(cur);
  return out;
}

// Parse a Postman-style `curl ...` string into {method,url,headers,body}.
// Handles backslash-newline continuations, -X/--request, -H/--header,
// -d/--data/--data-raw/--data-binary (implies POST), and URL = first non-flag.
// v1 deliberately skips --data-urlencode, -F multipart, -A; add in v2 if asked.
export function parseCurl(cmd: string): ParsedCurl {
  const normalized = cmd.replace(/\\\s*\n\s*/g, ' ').replace(/^\s*curl\s+/, '');
  const tok = splitQuoted(normalized);
  let method = '';
  let url = '';
  const headers: Record<string, string> = {};
  let body = '';
  for (let i = 0; i < tok.length; i++) {
    const t = tok[i];
    switch (t) {
      case '-X':
      case '--request':
        method = (tok[++i] || 'GET').toUpperCase();
        break;
      case '-H':
      case '--header': {
        const h = tok[++i] || '';
        const s = h.indexOf(':');
        if (s > 0) headers[h.slice(0, s).trim()] = h.slice(s + 1).trim();
        break;
      }
      case '-d':
      case '--data':
      case '--data-raw':
      case '--data-binary': {
        if (!method) method = 'POST';
        const d = tok[++i] || '';
        body = body ? body + '&' + d : d;
        break;
      }
      default:
        if (!url && !t.startsWith('-')) url = t;
      // unknown flags (-L, -v, --compressed, ...) are ignored in v1
    }
  }
  return { method: method || 'GET', url, headers, body: body || undefined };
}

// Render a parsed curl as a readable request block (pretty-prints JSON bodies).
export function beautify(p: ParsedCurl): string {
  const lines = [`${p.method} ${p.url}`];
  for (const [k, v] of Object.entries(p.headers)) lines.push(`${k}: ${v}`);
  if (p.body) {
    lines.push('');
    try { lines.push(JSON.stringify(JSON.parse(p.body), null, 2)); }
    catch { lines.push(p.body); }
  }
  return lines.join('\n');
}
