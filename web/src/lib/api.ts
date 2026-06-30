import type { BenchRec, FetchResult, ParsedCurl, ReplayResult, ReqRec, Session } from '../types';

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

// Benchmark batches (clubbed + filterable by tag), the request ledger, and replay.
export async function getBenches(tag?: string): Promise<BenchRec[]> {
  const q = tag ? `?tag=${encodeURIComponent(tag)}` : '';
  const r = await fetch(`${BASE}/benches${q}`);
  const j = await r.json();
  return j.benches || [];
}

export async function getRequests(n = 150): Promise<ReqRec[]> {
  const r = await fetch(`${BASE}/requests?n=${n}`);
  const j = await r.json();
  return j.requests || [];
}

export async function replay(id: number): Promise<ReplayResult> {
  const r = await fetch(`${BASE}/replay?id=${id}`, { method: 'POST' });
  return r.json();
}

// ── per-tab resources (8 watching itself + every tab) ────────────────────────
export interface TabProc {
  title: string; url: string; pid: number;
  mem_mb: number; cpu_ms: number; coresident_tabs: number; exact: boolean;
  cpu_pct?: number | null; // derived in the cockpit from cpu_ms deltas
}
export interface ProcInfo {
  tabs: TabProc[];
  gpu: { pid: number; mem_mb: number; cpu_ms: number; cpu_pct?: number | null } | null;
  parent_mem_mb: number;
  note: string;
}
// Per-tab memory/CPU via the chrome context (ChromeUtils.requestProcInfo).
// Exact per-tab when a tab is alone in its content process; GPU is one shared
// process. null when the collector was started without -gecko.
export async function procinfo(session = 'fox'): Promise<ProcInfo | null> {
  try {
    const r = await fetch(`${BASE}/procinfo?session=${encodeURIComponent(session)}`);
    if (!r.ok) return null;
    return await r.json();
  } catch { return null; }
}

// ── record → replay series (control driven through the wire, seat-attributed) ──
export interface SeriesInfo { name: string; frames: number; seats: string[]; modes: string[]; }
// CapFrame — one captured command, shown live IN the canvas (the recording deck)
// instead of only on the default feed page.
export interface CapFrame { seq: number; ts: string; physics: string; seat?: string; session?: string; method: string; url: string; status: number; }
export async function recordCtl(action: 'start' | 'stop' | '', name = '', seat = 'operator'): Promise<{ recording: boolean; name?: string; frames?: number; saved?: string; captured?: CapFrame[] }> {
  const q = new URLSearchParams();
  if (action) q.set('action', action);
  if (name) q.set('name', name);
  if (seat) q.set('seat', seat);
  const r = await fetch(`${BASE}/record?${q.toString()}`);
  return r.json();
}
// getFocus — the last seat anything acted on. 8 polls this and auto-foveates to
// it (attention follows action): driving a tab from the wire zooms 8 to its card.
export async function getFocus(): Promise<{ session: string; context: string; seq: number }> {
  try { const r = await fetch(`${BASE}/focus`); return r.json(); } catch { return { session: '', context: '', seq: 0 }; }
}
// addTab — open a new tab in a browser seat, protocol-aware. Firefox (BiDi)
// creates a context then navigates; Chrome (CDP) creates a target at the url.
// This is how the end user adds a tab to drive/record automation in.
export async function addTab(session: string, url: string, isCDP: boolean): Promise<void> {
  const u = url && !/^[a-z]+:\/\//i.test(url) ? `https://${url}` : url;
  if (isCDP) {
    await run(session, 'Target.createTarget', { url: u || 'about:blank' });
  } else {
    const r = (await run(session, 'browsingContext.create', { type: 'tab' })) as { result?: { context?: string } };
    const ctx = r?.result?.context;
    if (ctx && u) await run(session, 'browsingContext.navigate', { context: ctx, url: u, wait: 'complete' });
  }
}
export async function listSeries(): Promise<SeriesInfo[]> {
  try { const r = await fetch(`${BASE}/series`); return (await r.json()).series || []; } catch { return []; }
}
export async function replaySeries(name: string): Promise<{ fired: number; results: { seq: number; physics: string; seat: string; method: string; status: number; ok: boolean }[] }> {
  const r = await fetch(`${BASE}/replay-series?name=${encodeURIComponent(name)}`, { method: 'POST' });
  return r.json();
}
