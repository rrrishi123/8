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

  let physics: 'call' | 'channel' = 'channel';
  let method = f.method;
  let detail = '';
  if (f.method === 'http_request') {
    // an echoed curl/HTTP call (the efferent side)
    physics = 'call';
    method = String(p.http_method || 'GET');
    detail = p.error
      ? `${p.url || ''} → ERR ${p.error}`
      : `${p.url || ''} → ${p.status ?? ''} · ${p.latency_ms ?? '?'}ms`;
  } else if (f.method === 'run') {
    physics = 'call';
    method = 'run';
    detail = String(p.command || '');
  } else {
    // a real BiDi channel event
    detail = String(p.url || p?.request?.url || p?.response?.url || '');
  }
  const tab = typeof p.context === 'string' ? p.context : undefined;
  // what to screenshot when this row is clicked: a call to /session/{id} → that
  // session's device/browser; a BiDi event → the tab it came from.
  const sm = anyUrl.match(/\/session\/([\w-]+)/);
  const shot = sm ? { session: sm[1] } : (origin === 'BIDI' && tab ? { session: ev.session, context: tab } : undefined);
  return { id: ++seq, origin, physics, session: ev.session, method, detail: detail.slice(0, 200), at: Date.now(), raw: f.params ?? f, tab, shot };
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
