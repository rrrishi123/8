import type { FeedEvent, CaptureRow } from '../types';
import { feedUrl } from './api';

let seq = 0;

// Map one raw /feed event to a UI capture row. Real BiDi events only.
export function mapFrame(ev: FeedEvent): CaptureRow | null {
  const f = ev.frame;
  if (!f || !f.method) return null;
  const p = (f.params || {}) as Record<string, any>;
  const anyUrl = String(p.url || p?.request?.url || p?.response?.url || '');
  // Drop the cockpit's OWN infra traffic (collector :7070 + vite :8088) — it's
  // noise, and it feeds back on itself (the cockpit observing its own requests).
  if (ev.origin !== 'COLLECTOR' &&
      (/(127\.0\.0\.1|localhost):(7070|8088)/.test(anyUrl) || /^(data|blob):/.test(anyUrl))) return null;
  const origin: 'BIDI' | 'COLLECTOR' = ev.origin === 'COLLECTOR' ? 'COLLECTOR' : 'BIDI';

  // Honor an explicit physics from the collector (it knows the session's wire);
  // fall back to channel for raw BiDi events.
  let physics: 'call' | 'channel' = (ev as any).physics === 'call' ? 'call' : 'channel';
  let method = f.method;
  let detail = '';
  if (f.method === 'http_request') {
    physics = 'call';
    method = String(p.http_method || 'GET');
    detail = p.error
      ? `${p.url || ''} → ERR ${p.error}`
      : `${p.url || ''} → ${p.status ?? ''} · ${p.latency_ms ?? '?'}ms`;
  } else if (f.method === 'run') {
    physics = 'call';
    method = 'run';
    detail = String(p.command || '');
  } else if (origin === 'COLLECTOR') {
    // 8's own control call to a session (source / act / …): physics is already
    // set from ev.physics above; show the route + outcome.
    detail = `${p.route || method} → ${p.status ?? ''} · ${p.latency_ms ?? '?'}ms`;
  } else {
    // a real BiDi channel event
    detail = String(p.url || p?.request?.url || p?.response?.url || '');
  }
  const tab = typeof p.context === 'string' ? p.context : undefined;
  // what to screenshot when this row is clicked: a call to /session/{id} → that
  // session's device/browser; a BiDi event → the tab it came from.
  const sm = anyUrl.match(/\/session\/([\w-]+)/);
  const shot = sm ? { session: sm[1] } : (origin === 'BIDI' && tab ? { session: ev.session, context: tab } : undefined);
  const ledgerId = typeof p.ledger_id === 'number' ? p.ledger_id : undefined;
  const latencyMs = typeof p.latency_ms === 'number' ? p.latency_ms
    : (typeof p.latency_us === 'number' ? p.latency_us / 1000 : undefined);
  return { id: ++seq, origin, physics, session: ev.session, method, detail: detail.slice(0, 200), at: Date.now(), raw: f.params ?? f, tab, shot, ledgerId, latencyMs };
}

// #20 PUSH-DRIVEN REFRESH — ONE shared /feed subscription fans every
// <seat>.changed event out to the text cards, so they refresh on the collector's
// PUSH (the CHANNEL atom, #11/#12) instead of each polling every 2s. This is
// "push replaces poll" enacted at the CLIENT, not just the collector.
type ChangeCb = (kind: string, unit: string) => void;
let changeSubs: ChangeCb[] = [];
let changeES: EventSource | null = null;
function ensureChangeFeed() {
  if (changeES) return;
  changeES = new EventSource(feedUrl);
  changeES.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data);
      const m: string | undefined = ev?.frame?.method;
      if (typeof m === 'string' && m.endsWith('.changed')) {
        const kind = m.slice(0, -'.changed'.length); // tmux | nvim | daemons | tab
        const p = ev.frame.params || {};
        const unit = p.pane || (p.buf != null ? String(p.buf) : '') || p.context || '';
        for (const cb of changeSubs) cb(kind, unit);
      }
    } catch { /* keepalive / non-json */ }
  };
}
export function onSeatChange(cb: ChangeCb): () => void {
  ensureChangeFeed();
  changeSubs.push(cb);
  return () => {
    changeSubs = changeSubs.filter((f) => f !== cb);
    if (!changeSubs.length && changeES) { changeES.close(); changeES = null; }
  };
}

// Subscribe to the collector's /feed. Returns an unsubscribe fn.
export function openFeed(onRow: (row: CaptureRow) => void, onState: (live: boolean) => void): () => void {
  const es = new EventSource(feedUrl);
  es.onopen = () => onState(true);
  es.onerror = () => onState(false);
  es.onmessage = (e) => {
    try {
      const row = mapFrame(JSON.parse(e.data) as FeedEvent);
      if (row) onRow(row);
    } catch {
      /* ignore keepalive comments / non-JSON */
    }
  };
  return () => es.close();
}
