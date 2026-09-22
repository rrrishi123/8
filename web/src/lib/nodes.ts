// browser-nodes — the registry of browser HOSTS this 8 can see, from
// ~/.8/browser-nodes.json (written by browser-nodes-sync.sh from live docker
// port maps). The cockpit reads it through the collector (/nodes, when it grows
// one) or the dev server (/__8/browser-nodes.json, vite plugin). Nothing here is
// hardcoded to a machine: hosts/profiles are whatever the file says; seats and
// tabs are whatever the collector says; the join is by engine/mode/port.
const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

export interface BrowserNode {
  id: string; engine: string; mode: string; container?: string | null; profile?: string | null;
  view_url?: string | null; cdp_url?: string | null;
}
export interface Seat { id: string; physics: string; hub: string; status: string; stream?: string }
export interface Tab { context: string; url: string; title?: string; parked?: string }

// a source that answered 404 is remembered for the session (the collector has
// no /nodes yet): no 404 per poll in the console, one fetch per poll.
const gone = new Set<string>();
export async function fetchNodes(): Promise<BrowserNode[]> {
  for (const u of [`${BASE}/nodes`, `/__8/browser-nodes.json`]) {
    if (gone.has(u)) continue;
    try {
      const r = await fetch(u, { cache: 'no-store' });
      if (r.status === 404) { gone.add(u); continue; }
      if (!r.ok) continue;
      const j = await r.json();
      if (Array.isArray(j.nodes)) return j.nodes as BrowserNode[];
    } catch { /* try the next source */ }
  }
  return [];
}

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

/** join nodes ⨝ seats ⨝ tabs into hosts → profiles → tabs. Unmapped seats become hosts of their own. */
export function buildTree(nodes: BrowserNode[], seats: Seat[], tabsBy: Record<string, Tab[]>): HostNode[] {
  const hosts: HostNode[] = [];
  const claimed = new Set<string>();
  const allTabs: { seat: Seat; tab: Tab }[] = [];
  for (const s of seats) for (const t of tabsBy[s.id] || []) allTabs.push({ seat: s, tab: t });
  // 1. declared browser nodes
  const containerNodes = nodes.filter((n) => n.mode !== 'host');
  for (const n of nodes) {
    let seat: Seat | undefined;
    if (n.cdp_url) seat = seats.find((s) => isBrowserSeat(s) && s.stream === 'cdp' && sameListener(s.hub, n.cdp_url) && !claimed.has(s.id));
    if (!seat && n.mode === 'host') {
      // the host browser: the browser seat that is NOT any container's cdp listener
      seat = seats.find((s) => isBrowserSeat(s) && s.stream !== 'cdp' && !claimed.has(s.id)
        && !containerNodes.some((c) => sameListener(s.hub, c.cdp_url)));
    }
    if (seat) claimed.add(seat.id);
    // tabs that VIEW this node (a host-browser tab open on the container's view_url)
    const viewTabs = n.view_url ? allTabs.filter(({ tab }) => sameListener(tab.url, n.view_url)).map((x) => x.tab) : [];
    const tabs = seat ? (tabsBy[seat.id] || []) : [];
    const profile: ProfileNode = { key: `${n.id}/${n.profile || 'default'}`, label: n.profile || 'default', node: n, seat, tabs, viewTabs };
    hosts.push({ key: n.id, label: n.id, engine: n.engine, mode: n.mode, node: n, seat, tone: engineTone(n.engine, seat?.stream), profiles: [profile] });
  }
  // 2. seats no node declared (the file may be absent/stale): each is a host of its own
  for (const s of seats) {
    if (claimed.has(s.id) || s.status === 'disconnected') continue;
    const engine = s.stream === 'cdp' ? 'chromium' : isBrowserSeat(s) ? 'browser' : (s.hub.split(':')[0] || s.id);
    hosts.push({ key: `seat:${s.id}`, label: s.id, engine, mode: 'seat', seat: s, tone: engineTone(engine, s.stream),
      profiles: [{ key: `seat:${s.id}/${s.id}`, label: s.id, seat: s, tabs: tabsBy[s.id] || [], viewTabs: [] }] });
  }
  return hosts;
}
