// browser-nodes — the registry of browser HOSTS this 8 can see, served by the
// COLLECTOR at /nodes (#1147): ~/.8/browser-nodes.json (scripts/browser-nodes-sync.sh)
// merged with live docker port maps, JOINED to seats server-side on the
// broker's UPSTREAM (the socket it holds), never on its hub. A built cockpit
// (dist) therefore keeps the registry — no dev-server plugin in the path.
// Nothing here is hardcoded to a machine: hosts/profiles are whatever the
// collector says; seats and tabs are whatever the collector says.
const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

export interface BrowserNode {
  id: string; engine: string; mode: string; container?: string | null; profile?: string | null;
  view_url?: string | null; cdp_url?: string | null;
  // the join, done by the collector
  seat?: string; seat_status?: string; upstream?: string; attachable?: boolean; source?: string;
}
export interface Seat { id: string; physics: string; hub: string; status: string; stream?: string; upstream?: string }
export interface Tab { context: string; url: string; title?: string; parked?: string }

// a collector without /nodes (older build) answers 404: remembered for the
// session so it costs one fetch, not one 404 per poll.
let gone = false;
export async function fetchNodes(): Promise<BrowserNode[]> {
  if (gone) return [];
  try {
    const r = await fetch(`${BASE}/nodes`, { cache: 'no-store' });
    if (r.status === 404) { gone = true; return []; }
    if (!r.ok) return [];
    const j = await r.json();
    if (Array.isArray(j.nodes)) return j.nodes as BrowserNode[];
  } catch { /* collector away — keep the last registry */ }
  return [];
}

/** ATTACH: ask the collector to hold an unjoined node's browser (start a channel
 *  broker on its cdp_url). The node's seat appears on the next /sessions poll. */
export async function attachNode(id: string): Promise<{ ok: boolean; msg: string }> {
  try {
    const r = await fetch(`${BASE}/nodes/attach`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ id }) });
    const j = await r.json().catch(() => ({}));
    if (r.ok) return { ok: true, msg: j.already ? `already held as ${j.seat}` : `attached · seat ${j.seat}${j.bridged ? ' · bridged' : ''}` };
    return { ok: false, msg: j.error || `${r.status}` };
  } catch (e) { return { ok: false, msg: String(e) }; }
}

/** the LIVE desktop of a node: a container (Selkies) exposes it as view_url; a
 *  host browser has no remote desktop to embed. Not gated on a seat — the
 *  container is up whether or not a WebDriver/CDP session has been opened. */
export const liveView = (n?: BrowserNode | null): string | null =>
  n && n.mode !== 'host' && n.view_url ? n.view_url : null;

const portOf = (u?: string | null) => { try { return u ? new URL(u).port : ''; } catch { return ''; } };
const LOCAL = new Set(['0.0.0.0', '127.0.0.1', 'localhost', '::1', '[::1]']);
const hostOf = (u?: string | null) => { try { return u ? new URL(u).hostname : ''; } catch { return ''; } };
/** two urls point at the same listener: same port, and hosts equal or both local */
export function sameListener(a?: string | null, b?: string | null): boolean {
  const pa = portOf(a), pb = portOf(b);
  if (!pa || pa !== pb) return false;
  const ha = hostOf(a), hb = hostOf(b);
  return ha === hb || (LOCAL.has(ha) && LOCAL.has(hb));
}

// ── the tree: host → profile → tab ───────────────────────────────────────────
export interface ProfileNode { key: string; label: string; node?: BrowserNode; seat?: Seat; tabs: Tab[]; viewTabs: Tab[] }
export interface HostNode { key: string; label: string; engine: string; mode: string; node?: BrowserNode; seat?: Seat; tone: 'chrome' | 'firefox' | 'seat'; profiles: ProfileNode[] }

const isBrowserSeat = (s: Seat) => s.physics === 'channel' && s.stream !== 'text';
const engineTone = (engine: string, stream?: string): 'chrome' | 'firefox' | 'seat' =>
  /chrom/i.test(engine) || stream === 'cdp' ? 'chrome' : /fox|gecko/i.test(engine) ? 'firefox' : 'seat';

/** join nodes ⨝ seats ⨝ tabs into hosts → profiles → tabs. The node⨝seat join
 *  is the COLLECTOR's (node.seat); the fallback here joins on the seat's
 *  UPSTREAM (the socket its broker holds) — never on its hub, which is the
 *  broker itself and matches no browser listener. Unmapped seats become hosts
 *  of their own. */
export function buildTree(nodes: BrowserNode[], seats: Seat[], tabsBy: Record<string, Tab[]>): HostNode[] {
  const hosts: HostNode[] = [];
  const claimed = new Set<string>();
  const allTabs: { seat: Seat; tab: Tab }[] = [];
  for (const s of seats) for (const t of tabsBy[s.id] || []) allTabs.push({ seat: s, tab: t });
  // 1. declared browser nodes
  const containerNodes = nodes.filter((n) => n.mode !== 'host');
  const holds = (s: Seat, cdp?: string | null) => !!s.upstream && sameListener(s.upstream, cdp);
  for (const n of nodes) {
    let seat: Seat | undefined;
    if (n.seat) seat = seats.find((s) => s.id === n.seat && !claimed.has(s.id));
    if (!seat && n.cdp_url) seat = seats.find((s) => isBrowserSeat(s) && holds(s, n.cdp_url) && !claimed.has(s.id));
    if (!seat && n.mode === 'host') {
      // the host browser: the browser seat that holds no container's devtools listener
      seat = seats.find((s) => isBrowserSeat(s) && s.stream !== 'cdp' && !claimed.has(s.id)
        && !containerNodes.some((c) => holds(s, c.cdp_url)));
    }
    if (seat) claimed.add(seat.id);
    // tabs that VIEW this node (a host-browser tab open on the container's view_url)
    const viewTabs = n.view_url ? allTabs.filter(({ tab }) => sameListener(tab.url, n.view_url)).map((x) => x.tab) : [];
    const tabs = seat ? (tabsBy[seat.id] || []) : [];
    const profile: ProfileNode = { key: `${n.id}/${n.profile || 'default'}`, label: n.profile || 'default', node: n, seat, tabs, viewTabs };
    hosts.push({ key: n.id, label: n.id, engine: n.engine, mode: n.mode, node: n, seat, tone: engineTone(n.engine, seat?.stream), profiles: [profile] });
  }
  // 2. seats no node declared (the registry may be absent/stale): each is a host of its own
  for (const s of seats) {
    if (claimed.has(s.id) || s.status === 'disconnected') continue;
    const engine = s.stream === 'cdp' ? 'chromium' : isBrowserSeat(s) ? 'browser' : (s.hub.split(':')[0] || s.id);
    hosts.push({ key: `seat:${s.id}`, label: s.id, engine, mode: 'seat', seat: s, tone: engineTone(engine, s.stream),
      profiles: [{ key: `seat:${s.id}/${s.id}`, label: s.id, seat: s, tabs: tabsBy[s.id] || [], viewTabs: [] }] });
  }
  return hosts;
}
