import type { FetchResult, ParsedCurl, Session } from '../types';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

export const feedUrl = `${BASE}/feed`;

// A screenshot of a session (device for call sessions, tab for channel) — the
// wire's native screenshot, via the collector. Returns a data URL.
export async function shot(session: string, context?: string): Promise<string> {
  const q = context ? `&context=${encodeURIComponent(context)}` : '';
  const r = await fetch(`${BASE}/shot?session=${encodeURIComponent(session)}${q}`);
  const j = await r.json();
  return j.data || '';
}

export async function health(): Promise<{ alive: boolean; sessions: string[] }> {
  const r = await fetch(`${BASE}/health`);
  return r.json();
}

// The live session registry — held brokers + every session in ~/.8/sessions.json
// (sessions any client created via http-mcp). This is the intersection.
export async function listSessions(): Promise<Session[]> {
  const r = await fetch(`${BASE}/sessions`);
  const j = await r.json();
  return j.sessions || [];
}

// The curl hit: execute a raw HTTP request server-side (no browser CORS).
export async function fetchUrl(req: ParsedCurl): Promise<FetchResult> {
  const r = await fetch(`${BASE}/fetch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  return r.json();
}

// Run a command on a session via its held broker.
export async function run(session: string, method: string, params: Record<string, unknown>): Promise<unknown> {
  const r = await fetch(`${BASE}/run?session=${encodeURIComponent(session)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ method, params }),
  });
  return r.json();
}
