import { useEffect, useMemo, useRef, useState } from 'react';
import { PaneCockpit } from './PaneCockpit';
import { PaneLive } from './PaneLive';
import { resetDrag } from '../lib/useDrag';
import { Viewport } from './Viewport';
import { Resources } from './Resources';
import { PasteCurl } from './PasteCurl';
import { Matrix } from './Matrix';
import { useInstruments, WorkBody, ClockBody, InnerBody, PortalBody } from './Instruments';
import { useWireRows, WireRows } from './WireLog';
import { CardFrame, HostBody, ProfileBody, BudgetBody } from './Card';
import { useLocal } from './Dock';
import { procinfo, recordCtl, listSeries, replaySeries, addTab, getFocus, type SeriesInfo, type CapFrame } from '../lib/api';
import { GRID, SeqLedger, packWorld, onScreen, syncFontFromCSS, setUnit, levelForZoom, LEVELS, type Card, type Lane, type Level, type CardKind } from '../lib/cards';
import { fetchNodes, buildTree, liveView, type BrowserNode, type Seat, type Tab, type HostNode, type ProfileNode } from '../lib/nodes';
import { useReportedTexts, textOf } from '../lib/cardText';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// SELF_ID — a per-tab id in the URL hash so a cockpit can tell its OWN tab apart
// from a SIBLING cockpit tab (both are localhost:8088). Set once, synchronously,
// at module load so it exists before the first /tabs poll. Enables the
// two-cockpit fan-out (drive one 8-tab, watch it rise in the other's canvas).
const SELF_ID = (() => {
  try {
    let id = new URLSearchParams(location.hash.replace(/^#/, '')).get('c');
    if (!id) { id = Math.random().toString(36).slice(2, 8); location.hash = 'c=' + id; }
    return id;
  } catch { return 'self'; }
})();

// PRETEXT COCKPIT (#1132). Every live target — and every gauge — is a CARD in one
// grammar (src/lib/cards.ts), sized by pretext from its text, by its aspect, or
// — structured cards (host/profile/panes/budget) — by ONE DOM measurement of the
// real body (#1147). Cards are packed masonry-style into LANES (one per host /
// profile / seat / type), lanes stand side by side, and the camera pans/zooms
// the world like a map. SEMANTIC ZOOM: far out you see hosts (browser-nodes.json),
// closer you see profiles, close you see tabs (BiDi getTree). Cards you can't see
// aren't mounted (occlusion); cards too small to read collapse to a label (LOD).
// Arrival order is layout order — a newcomer lands AFTER everything placed, never
// on top of the bottom card's spot.
const TYPE_LANES: { key: string; label: string; kinds: CardKind[] }[] = [
  { key: 'type:minds', label: 'minds · panes', kinds: ['panes', 'heart'] },
  { key: 'type:work', label: 'work · record', kinds: ['tasks', 'record', 'compose'] },
  { key: 'type:gauges', label: 'gauges', kinds: ['budget', 'wire', 'resources', 'clock', 'matrix', 'inner', 'portal'] },
];
const KIND_TITLE: Record<string, string> = {
  panes: 'panes · send', heart: 'pane · live', tasks: 'work', record: 'recording', compose: 'compose', budget: 'budget',
  wire: 'wire · efferent', resources: 'resources', clock: 'clock · experiri', matrix: 'surfaces × senses', inner: 'inner host', portal: 'portal',
};

const hostOf = (u: string) => { try { return new URL(u).host.replace(/^www\./, ''); } catch { return u || 'tab'; } };

export function Canvas({ session, focusKey }: { session: string | null; focusKey?: string }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [cam, setCam] = useLocal<{ x: number; y: number; z: number }>('cam', { x: 60, y: 30, z: 0.42 });
  const [seats, setSeats] = useState<Seat[]>([]);
  const [tabsBy, setTabsBy] = useState<Record<string, Tab[]>>({});
  const [nodes, setNodes] = useState<BrowserNode[]>([]);
  const [aspectBy, setAspectBy] = useLocal<Record<string, number>>('aspectBy', {});
  const [hudBy, setHudBy] = useState<Record<string, { mem?: number; cpu?: number | null }>>({});
  // ADAPTIVE APERTURE (view side): the seat's PARENT memory, polled with procinfo.
  // Capture is what bloats the parent (drawSnapshot surfaces), so under pressure
  // the witness dims its own eyes BEFORE the collector must flush/park and long
  // before the watchdog's 4500 recycle: hero 3fps → 1fps, ambient 0.4 → frozen.
  const [parentMem, setParentMem] = useState(0);
  const [pinnedKey, setPinnedKey] = useState(''); // the HERO card (explicit pin, not center)
  useEffect(() => { if (focusKey) setPinnedKey(focusKey); }, [focusKey]);
  const [spreadBy, setSpreadBy] = useLocal<Record<string, boolean>>('spreadBy', {}); // false = solitaire-stacked lane
  // LAYOUT IS THE DEFAULT, POSITION IS THE OPERATOR'S: a drag adds a persisted
  // per-lane OFFSET on top of the computed layout; "⌂ layout" forgets all offsets.
  const [posBy, setPosBy] = useLocal<Record<string, { x: number; y: number }>>('posBy', {});
  const dragLane = useRef<{ key: string; px: number; py: number; bx: number; by: number } | null>(null);
  const [showSelf, setShowSelf] = useLocal<boolean>('showSelf', false); // reflexive: let this 8 SEE its own tab
  const [hidden, setHidden] = useLocal<Record<string, boolean>>('cardHidden', {});
  const [levelPick, setLevelPick] = useLocal<'auto' | Level>('level', 'auto');
  const [menu, setMenu] = useState(false);
  const [theater, setTheater] = useState(false);
  useEffect(() => { if (!theater) return; const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setTheater(false); }; window.addEventListener('keydown', onKey); return () => window.removeEventListener('keydown', onKey); }, [theater]);
  const [addFor, setAddFor] = useState('');
  const [addUrl, setAddUrl] = useState('https://www.airbnb.com');
  const [liveKeys, setLiveKeys] = useState<string[]>([]); // recent-set: last 3 focused fox cards stay LIVE
  const [hoverKey, setHoverKey] = useState('');           // hover promotes a frozen tile to live
  const prevCpu = useRef<Record<string, { c: number; t: number }>>({});
  const rectsRef = useRef<Record<string, { x: number; y: number; w: number; h: number }>>({});
  const lastFocusSeq = useRef(-1);
  const ledger = useMemo(() => new SeqLedger(), []);
  useReportedTexts(); // re-layout when a body reports new text
  useEffect(() => { syncFontFromCSS(); }, []);
  // viewport size WITHOUT layout reads: ResizeObserver hands us the content box.
  const [vp, setVp] = useState({ w: 0, h: 0 });
  useEffect(() => {
    const el = wrap.current; if (!el) return;
    const ro = new ResizeObserver((es) => { const r = es[0]?.contentRect; if (r) { setUnit(r.width); setVp({ w: r.width, h: r.height }); } });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // RECORD → REPLAY, shown IN the canvas
  const [rec, setRec] = useState<{ recording: boolean; name?: string; frames?: number; captured?: CapFrame[] }>({ recording: false });
  const [series, setSeries] = useState<SeriesInfo[]>([]);
  const [recName, setRecName] = useState('canvas-1');
  const recRef = useRef(false);
  useEffect(() => {
    let alive = true;
    let hidTick = 0;
    const tick = async () => {
      if (!alive) return;
      hidTick++;
      if (!document.hidden || hidTick % 4 === 0 || recRef.current) {
        const r = await recordCtl(''); setRec(r); recRef.current = !!r.recording;
      }
      if (alive) window.setTimeout(tick, recRef.current ? 1000 : 4000);
    };
    tick();
    listSeries().then(setSeries);
    let sTick = 0;
    const st = window.setInterval(() => { sTick++; if (!document.hidden || sTick % 3 === 0) listSeries().then(setSeries); }, 8000);
    return () => { alive = false; clearInterval(st); };
  }, []);
  const toggleRec = async () => {
    if (rec.recording) await recordCtl('stop'); else await recordCtl('start', recName, 'ai');
    recordCtl('').then(setRec); listSeries().then(setSeries);
  };

  // the browser-node registry (hosts/profiles) — slow poll; it changes when docker does
  useEffect(() => {
    let alive = true;
    const pull = () => fetchNodes().then((n) => { if (alive) setNodes(n); });
    pull();
    const t = window.setInterval(pull, 15000);
    return () => { alive = false; clearInterval(t); };
  }, []);

  useEffect(() => {
    const load = async () => {
      try {
        const j = await (await fetch(`${BASE}/sessions`)).json();
        const live: Seat[] = ((j.sessions as Seat[]) || []).filter((s) => s.status !== 'disconnected');
        setSeats(live);
        const tb: Record<string, Tab[]> = {};
        await Promise.all(live.filter((s) => s.physics === 'channel').map(async (s) => {
          try {
            const t = await (await fetch(`${BASE}/tabs?session=${encodeURIComponent(s.id)}`)).json();
            // Exclude only THIS cockpit's OWN tab (by its self-id), not every
            // localhost:8088 tab — a SIBLING cockpit tab appears as a card.
            tb[s.id] = (t.tabs || []).filter((x: Tab) => {
              if (showSelf) return true;
              const u = String(x.url || '');
              const mine = u.includes(location.host) && u.includes('c=' + SELF_ID);
              const ownNoId = u.replace(/#.*$/, '') === location.href.replace(/#.*$/, '') && !u.includes('c=');
              return !mine && !ownNoId;
            });
          } catch { tb[s.id] = []; }
        }));
        setTabsBy(tb);
        const fox = live.find((s) => s.physics === 'channel' && s.stream !== 'cdp' && s.stream !== 'text');
        if (fox) {
          const p = await procinfo(fox.id);
          if (p) {
            const now = Date.now();
            const map: Record<string, { mem?: number; cpu?: number | null }> = {};
            p.tabs.forEach((t) => {
              const pr = prevCpu.current['p' + t.pid];
              prevCpu.current['p' + t.pid] = { c: t.cpu_ms, t: now };
              const cpu = pr && now > pr.t ? Math.max(0, ((t.cpu_ms - pr.c) / (now - pr.t)) * 100) : null;
              map[t.url] = { mem: t.mem_mb, cpu };
            });
            setHudBy(map);
            setParentMem(p.parent_mem_mb || 0);
          }
        }
      } catch { /* keep last */ }
    };
    load();
    // hidden ≠ dead: agents keep driving with no human window — hidden only SLOWS the poll.
    let n = 0;
    const t = window.setInterval(() => { n++; if (!document.hidden || n % 4 === 0) load(); }, 4000);
    return () => clearInterval(t);
  }, [showSelf]);

  // ── the tree: hosts → profiles → tabs ────────────────────────────────────────
  const tree = useMemo(() => buildTree(nodes, seats, tabsBy), [nodes, seats, tabsBy]);
  const level: Level = levelPick === 'auto' ? levelForZoom(cam.z) : levelPick;

  // ATTENTION FOLLOWS ACTION: poll /focus; when it changes, pin that card as hero
  // and zoom to it (which also drops the semantic zoom to the tabs level).
  useEffect(() => {
    let alive = true;
    let hidTick = 0;
    const poll = async () => {
      if (!alive) return;
      hidTick++;
      if (!document.hidden || hidTick % 4 === 0) {
        const f = await getFocus();
        if (f.seq > 0 && f.seq !== lastFocusSeq.current) {
          lastFocusSeq.current = f.seq;
          // an explicit hosts/profiles pick is the operator's choice of altitude: keep it
          if (levelPickRef.current !== 'auto' && levelPickRef.current !== 'tabs') { if (alive) window.setTimeout(poll, 1500); return; }
          const key = f.session + (f.context || '');
          setPinnedKey(key);
          setCam((c) => ({ ...c, z: 0.85 })); // tabs level; the card exists after the next render
          window.setTimeout(() => {
            const rect = rectsRef.current[key];
            if (rect && vpRef.current.w) { const z = 0.85; setCam({ z, x: vpRef.current.w / 2 - (rect.x + rect.w / 2) * z, y: vpRef.current.h / 2 - (rect.y + rect.h / 2) * z }); }
          }, 140);
        }
      }
      if (alive) window.setTimeout(poll, 1500);
    };
    poll();
    return () => { alive = false; };
  }, [setCam]);
  const vpRef = useRef(vp); vpRef.current = vp;
  const levelPickRef = useRef(levelPick); levelPickRef.current = levelPick;

  const isHidden = (k: string) => !!hidden[k];
  const instr = useInstruments(true);
  const wireRows = useWireRows(24);

  // ── BUILD THE LANES for this level ───────────────────────────────────────────
  const lanes: Lane[] = [];
  const cellTitle = (s: Seat, t: Tab) => s.stream === 'text' ? (t.title || `${t.context} · ${t.url.replace(/^\w+:\/\//, '')}`) : hostOf(t.url);
  const tone = (h: HostNode) => h.tone;
  // DRILL INTO a browser node: land on its tabs lane and pin its card as hero —
  // the live desktop card when the node has one, else its first tab. A click
  // never zooms OUT (the old "no session" card sent you back to profiles).
  const LIVE_ASPECT = 1.6; // the desktop is a picture card, sized like a tab viewport
  const heroOf = (p: ProfileNode) => (p.seat && p.tabs.length && !liveView(p.node)) ? p.seat.id + (p.tabs[0].context || 'blank-0') : 'profile:' + p.key;
  const drill = (p: ProfileNode) => { setPinnedKey(heroOf(p)); setLevelPick('tabs'); focusLane(p.key); };
  const drillHost = (h: HostNode) => { if (h.profiles.length === 1) drill(h.profiles[0]); else { setLevelPick('profiles'); focusLane(h.key); } };
  if (level === 'hosts') {
    lanes.push({ key: 'hosts', label: 'hosts', kind: 'host', tone: 'type', badge: `${tree.length} host${tree.length === 1 ? '' : 's'}`,
      cards: tree.map((h) => ({ key: 'host:' + h.key, lane: 'hosts', kind: 'host' as const, title: h.label, meta: `${h.engine} · ${h.mode}${liveView(h.node) ? ' · live' : ''}`,
        aspect: liveView(h.node) ? LIVE_ASPECT : undefined, measure: liveView(h.node) ? undefined : 'dom' as const,
        node: <HostBody host={h} onZoom={() => drillHost(h)} /> })) });
  } else if (level === 'profiles') {
    for (const h of tree) lanes.push({ key: h.key, label: h.label, kind: 'profile', tone: tone(h), badge: `${h.profiles.length} profile${h.profiles.length === 1 ? '' : 's'}`,
      cards: h.profiles.map((p) => ({ key: 'profile:' + p.key, lane: h.key, kind: 'profile' as const, title: p.label, meta: `${h.label} · ${p.tabs.length} tabs${liveView(p.node) ? ' · live' : ''}`,
        aspect: liveView(p.node) ? LIVE_ASPECT : undefined, measure: liveView(p.node) ? undefined : 'dom' as const, span: liveView(p.node) ? 2 : undefined,
        node: <ProfileBody host={h} profile={p} onZoom={() => drill(p)} /> })) });
  } else {
    for (const h of tree) for (const p of h.profiles) {
      const laneKey = p.key;
      const label = h.profiles.length > 1 || p.label !== h.label ? (p.label === 'host' || p.label === h.label ? h.label : `${h.label} · ${p.label}`) : h.label;
      const cards: Card[] = [];
      const seenCtx = new Set<string>();
      const live = liveView(p.node);
      if (live) {
        // the container's REAL desktop, first in its lane — with or without a
        // seat (the desktop is up regardless; tabs arrive when a session does)
        cards.push({ key: 'profile:' + p.key, lane: laneKey, kind: 'profile', title: p.label, meta: `${h.label} · live desktop`, span: 2, aspect: LIVE_ASPECT,
          node: <ProfileBody host={h} profile={p} onZoom={() => drill(p)} /> });
      }
      if (p.seat) {
        const s = p.seat;
        p.tabs.filter((t) => { if (!t.context) return true; if (seenCtx.has(t.context)) return false; seenCtx.add(t.context); return true; })
          .forEach((t, ti) => {
            const key = s.id + (t.context || `blank-${ti}`);
            const device = false;
            cards.push({ key, lane: laneKey, kind: 'tab', title: cellTitle(s, t), meta: t.url, bare: true, span: device ? 1 : 2,
              aspect: aspectBy[key] || 1.6, node: null });
          });
        if (!cards.some((c) => c.kind === 'tab') && s.physics !== 'channel') {
          const device = !!s.stream;
          cards.push({ key: s.id, lane: laneKey, kind: 'seat', title: `${device ? 'device' : 'seat'} · ${s.id.slice(0, 8)}`, bare: true, span: device ? 1 : 2, aspect: aspectBy[s.id] || (device ? 0.46 : 1.6), node: null });
        }
      }
      if (!cards.length) {
        // a declared host with no drivable session yet — keep it visible as its
        // profile card, which OFFERS ⚡ attach when the node has a cdp_url (#1147)
        cards.push({ key: 'profile:' + p.key, lane: laneKey, kind: 'profile', title: p.label, meta: `${h.label} · ${p.node?.attachable ? 'no session · attachable' : 'no session'}`, measure: 'dom',
          node: <ProfileBody host={h} profile={p} onZoom={() => drill(p)} /> });
      }
      // a session-less host holds one placeholder profile card: 2 columns, not the
      // 8 a browser lane reserves (a session arriving re-lays the lane anyway)
      const nTabs = cards.filter((c) => c.kind === 'tab').length;
      lanes.push({ key: laneKey, label, kind: h.seat && h.seat.physics === 'channel' && h.seat.stream !== 'text' ? 'browser' : 'seat', tone: tone(h),
        cols: p.seat ? undefined : 2,
        badge: p.seat ? `${nTabs} tab${nTabs === 1 ? '' : 's'}${live ? ' · live' : ''}` : (live ? 'live · no session' : 'no session'), cards });
    }
  }
  // type lanes (the gauges) — at every level, so the cockpit never loses its instruments
  const typeCards: Record<string, Card> = {
    panes: { key: 'panes', lane: 'type:minds', kind: 'panes', title: KIND_TITLE.panes, meta: 'broadcast one prompt to chosen claude panes', measure: 'dom', node: <PaneCockpit cardKey="panes" /> },
    heart: { key: 'heart', lane: 'type:minds', kind: 'heart', title: KIND_TITLE.heart, meta: 'one pane as a pod — measured context, process, inspector', measure: 'dom', node: <PaneLive cardKey="heart" /> },
    tasks: { key: 'tasks', lane: 'type:work', kind: 'tasks', title: KIND_TITLE.tasks, meta: `${instr.openCount} open`, node: <WorkBody cardKey="tasks" i={instr} /> },
    record: { key: 'record', lane: 'type:work', kind: 'record', title: KIND_TITLE.record, meta: rec.recording ? `● ${rec.name} · ${rec.frames ?? 0} cmds` : '○ idle', text: recText(rec),
      node: <RecBody rec={rec} /> },
    compose: { key: 'compose', lane: 'type:work', kind: 'compose', title: KIND_TITLE.compose, meta: 'record → replay · manual http + ws', text: ['record', 'series', 'manual compose (http + ws)', '\n\n\n', 'fire'].join('\n'), node: <PasteCurl /> },
    budget: { key: 'budget', lane: 'type:gauges', kind: 'budget', title: KIND_TITLE.budget, meta: 'claude · codex, segregated', measure: 'dom', node: <BudgetBody /> },
    wire: { key: 'wire', lane: 'type:gauges', kind: 'wire', title: KIND_TITLE.wire, meta: `${wireRows.length} on the wire`, node: <WireRows rows={wireRows} cardKey="wire" /> },
    resources: { key: 'resources', lane: 'type:gauges', kind: 'resources', title: KIND_TITLE.resources, meta: 'per-tab memory + cpu', node: <Resources session={session} cardKey="resources" /> },
    clock: { key: 'clock', lane: 'type:gauges', kind: 'clock', title: KIND_TITLE.clock, meta: instr.now, node: <ClockBody cardKey="clock" i={instr} /> },
    matrix: { key: 'matrix', lane: 'type:gauges', kind: 'matrix', title: KIND_TITLE.matrix, meta: 'the map of the unfound', node: <Matrix cardKey="matrix" /> },
    inner: { key: 'inner', lane: 'type:gauges', kind: 'inner', title: KIND_TITLE.inner, meta: 'containers + colima', node: <InnerBody cardKey="inner" i={instr} /> },
    portal: { key: 'portal', lane: 'type:gauges', kind: 'portal', title: KIND_TITLE.portal, meta: 'federated 8 nodes', node: <PortalBody cardKey="portal" i={instr} /> },
  };
  for (const c of Object.values(typeCards)) if (c.text === undefined && c.measure !== 'dom') c.text = textOf(c.key) ?? `${c.title}\n\n\n`;
  for (const tl of TYPE_LANES) lanes.push({ key: tl.key, label: tl.label, kind: 'type', tone: 'type', cols: 2, cards: tl.kinds.map((k) => typeCards[k]) });

  // hidden cards leave the layout; stacked lanes; arrival order; hero
  for (const l of lanes) { l.cards = ledger.sort(l.cards.filter((c) => !isHidden(c.key))); l.stacked = spreadBy[l.key] === false && l.cards.length > 1; }
  const ordered = ledger.sort(lanes).filter((l) => l.cards.length > 0);
  const allCards = ordered.flatMap((l) => l.cards);
  const tabCards = allCards.filter((c) => c.kind === 'tab' || c.kind === 'seat');
  const heroKey = (pinnedKey && allCards.some((c) => c.key === pinnedKey)) ? pinnedKey : (tabCards[0]?.key || '');
  for (const c of allCards) c.hero = c.key === heroKey;
  useEffect(() => {
    if (!heroKey) return;
    setLiveKeys((prev) => (prev[0] === heroKey ? prev : [heroKey, ...prev.filter((k) => k !== heroKey)].slice(0, 3)));
  }, [heroKey]);
  const liveSet = new Set([heroKey, ...liveKeys, hoverKey].filter(Boolean));

  // ── PACK ────────────────────────────────────────────────────────────────────
  const world = packWorld(ordered, heroKey, posBy);
  rectsRef.current = {};
  for (const L of world.lanes) for (const p of L.cards) rectsRef.current[p.key] = { x: p.x, y: p.y, w: p.w, h: p.h };
  const laneRect = (key: string) => world.lanes.find((L) => L.lane.key === key);
  const goto = (r: { x: number; y: number; w: number; h: number }, z: number) => { const w = vpRef.current.w || 1200, h = vpRef.current.h || 800; setCam({ z, x: w / 2 - (r.x + r.w / 2) * z, y: h / 2 - (r.y + r.h / 2) * z }); };
  const pendingLane = useRef('');
  function focusLane(key: string) { pendingLane.current = key; }
  useEffect(() => { // after a level change that targeted a lane, land on it
    const k = pendingLane.current; if (!k) return;
    const L = world.lanes.find((x) => x.lane.key === k || x.lane.cards.some((c) => c.key.endsWith(k)));
    if (L) { pendingLane.current = ''; landed.current = true; goto({ x: L.x, y: L.y, w: L.w, h: L.h + GRID.laneHead }, Math.min(0.9, Math.max(0.45, (vpRef.current.w || 1200) / (L.w + 200)))); }
  });
  // when the effective level flips (zoom crossed a threshold), keep the camera on the world
  // — unless this very commit LANDED on a lane (a drill): the landing effect above
  // runs first and has already cleared pendingLane; without `landed` the recentre
  // below would override it and the drilled-into lane would be off-screen.
  const prevLevel = useRef(level);
  const landed = useRef(false);
  useEffect(() => {
    if (prevLevel.current === level) return;
    prevLevel.current = level;
    if (pendingLane.current || landed.current) { landed.current = false; return; }
    // the new level's world has a different height: keep x centred on it and
    // clamp y so the top of the world is on screen (a deep scroll from the
    // previous level would otherwise leave every card occluded).
    setCam((c) => { const vh = vpRef.current.h || 800; const minY = Math.min(40, vh - world.h * c.z); return { ...c, x: (vpRef.current.w || 1200) / 2 - (world.w / 2) * c.z, y: Math.max(minY, Math.min(c.y, 40)) }; });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [level]);

  // KEEP THE WORLD IN VIEW: a persisted camera from a taller level (or a lane
  // that vanished) can leave the viewport over empty space with every card
  // occluded. When the viewport and the world don't intersect, snap to the top-
  // left. Runs on world/viewport/level changes only — panning off the edge by
  // hand is the operator's, this never fights a drag.
  const camRef = useRef(cam); camRef.current = cam;
  useEffect(() => {
    const v = vpRef.current, c = camRef.current;
    if (!v.w || !world.h || pendingLane.current) return;
    const vx = -c.x / c.z, vy = -c.y / c.z, vw = v.w / c.z, vh = v.h / c.z;
    const intersects = vx < world.w && vx + vw > 0 && vy < world.h && vy + vh > 0;
    if (!intersects) setCam((k) => ({ ...k, x: 40, y: 40 }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [world.w, world.h, vp.w, vp.h, level]);

  // LEVEL-OF-DETAIL: displayed width → quantized capture width bucket
  const lodBucket = (px: number) => { for (const v of [160, 240, 360, 540, 768, 1024, 1440]) if (px <= v) return v; return 1600; };

  useEffect(() => {
    const el = wrap.current; if (!el) return;
    const onWheel = (e: WheelEvent) => {
      // each card/panel body scrolls ITSELF, not the map; only the world pans/zooms.
      if ((e.target as HTMLElement).closest('.card-b.scroll, .vp-text, .vp-tmux, .vp-interactive, .rec-bar, .minimap, .persp-bar, .cards-menu')) return;
      e.preventDefault();
      if (e.ctrlKey || e.metaKey) {
        const r = (e.currentTarget as HTMLElement); const mx = e.clientX - r.offsetLeft, my = e.clientY - r.offsetTop;
        setCam((c) => { const nz = Math.max(0.06, Math.min(3, c.z * (e.deltaY < 0 ? 1.06 : 0.94))); const k = nz / c.z; return { z: nz, x: mx - (mx - c.x) * k, y: my - (my - c.y) * k }; });
      } else {
        setCam((c) => ({ ...c, x: c.x - e.deltaX, y: c.y - e.deltaY }));
      }
    };
    const onDown = (e: PointerEvent) => {
      if ((e.target as HTMLElement).closest('button, input, select, textarea, a, .card-b.scroll, .card-acts, .seeing-tabs, .tab-pick, .series-row, .rec-btn, .curl-in, .persp-bar, .deck-head, .cap-card, .vp-interactive, .cards-menu')) return;
      el.style.cursor = 'grabbing';
      let lx = e.clientX, ly = e.clientY;
      const move = (ev: PointerEvent) => { setCam((c) => ({ ...c, x: c.x + (ev.clientX - lx), y: c.y + (ev.clientY - ly) })); lx = ev.clientX; ly = ev.clientY; };
      const up = () => { el.style.cursor = ''; window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', up); };
      window.addEventListener('pointermove', move); window.addEventListener('pointerup', up);
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    el.addEventListener('pointerdown', onDown);
    return () => { el.removeEventListener('wheel', onWheel); el.removeEventListener('pointerdown', onDown); };
  }, [setCam]);

  const persp = {
    p1: () => { const r = rectsRef.current[heroKey]; if (r) goto(r, 0.9); },
    p2: () => goto({ x: GRID.x0, y: GRID.y0, w: world.w - GRID.x0, h: Math.min(world.h - GRID.y0, 1200) }, 0.45),
    bird: () => { const w = vpRef.current.w || 1200, h = vpRef.current.h || 800; const z = Math.max(0.06, Math.min(0.3, Math.min(w / world.w, h / world.h) * 0.92)); goto({ x: 0, y: 0, w: world.w, h: world.h }, z); },
  };
  const cycleLevel = () => { const order: ('auto' | Level)[] = ['auto', ...LEVELS]; setLevelPick(order[(order.indexOf(levelPick) + 1) % order.length]); };
  const showCard = (key: string) => { setHidden((h) => ({ ...h, [key]: false })); window.setTimeout(() => { const r = rectsRef.current[key]; if (r) goto(r, 0.8); }, 60); };

  // NB: no window.prompt — the cockpit runs inside a WebDriver-controlled Firefox,
  // which auto-DISMISSES native dialogs. Use an inline input.
  const newTab = async (seat: Seat, url: string) => {
    const u = (url || '').trim();
    if (!u) return;
    setAddFor('');
    await addTab(seat.id, u, seat.stream === 'cdp');
  };
  const seatOfLane = (key: string): Seat | undefined => { for (const h of tree) for (const p of h.profiles) if (p.key === key) return p.seat; return undefined; };

  let mounted = 0, ghosts = 0;
  return (
    <div className="canvas-wrap" ref={wrap}>
      <div className="world" style={{ transform: `translate(${cam.x}px,${cam.y}px) scale(${cam.z})` }}>
        {/* lane heads — name · count · fan/stack · + tab · zoom */}
        {world.lanes.map((L) => {
          const l = L.lane; const seat = seatOfLane(l.key);
          return (
            <div key={'h-' + l.key} className="deck-head" style={{ left: L.x, top: L.y, width: L.w }}
              onPointerDown={(e) => {
                if ((e.target as HTMLElement).closest('button, input')) return;
                try { (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId); } catch { /* */ }
                const cur = posBy[l.key] || { x: 0, y: 0 };
                dragLane.current = { key: l.key, px: e.clientX, py: e.clientY, bx: cur.x, by: cur.y };
              }}
              onPointerMove={(e) => {
                const g = dragLane.current; if (!g || g.key !== l.key) return;
                setPosBy((p) => ({ ...p, [g.key]: { x: g.bx + (e.clientX - g.px) / cam.z, y: g.by + (e.clientY - g.py) / cam.z } }));
              }}
              onPointerUp={() => { dragLane.current = null; }}>
              <span className={`deck-name ${l.tone || 'seat'}`}>{l.label}</span>
              {l.badge && <span className="deck-count">{l.badge}</span>}
              {!l.badge && <span className="deck-count">{l.cards.length} card{l.cards.length === 1 ? '' : 's'}</span>}
              {l.cards.length > 1 && (
                <button className="deck-btn" title={l.stacked ? 'fan the deck out — masonry' : 'stack the deck — solitaire, hero on top'}
                  onClick={() => setSpreadBy((p) => ({ ...p, [l.key]: !!l.stacked }))}>{l.stacked ? '⊞ fan' : '▣ stack'}</button>
              )}
              {l.kind === 'browser' && seat && (addFor === l.key
                ? <input className="deck-add-in" autoFocus value={addUrl}
                    onChange={(e) => setAddUrl(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') newTab(seat, addUrl); else if (e.key === 'Escape') setAddFor(''); }}
                    placeholder="url + Enter" />
                : <button className="deck-btn add" title="open a new tab in this browser to drive/record" onClick={() => setAddFor(l.key)}>+ tab</button>)}
              {level !== 'tabs' && l.kind !== 'type' && (
                <button className="deck-btn" title={`zoom in → ${level === 'hosts' ? 'profiles' : 'tabs'}`}
                  onClick={() => { setLevelPick(level === 'hosts' ? 'profiles' : 'tabs'); focusLane(l.key); }}>▸ {level === 'hosts' ? 'profiles' : 'tabs'}</button>
              )}
            </div>
          );
        })}
        {world.lanes.flatMap((L) => L.cards.map((p) => {
          const c = p.card;
          const isVp = c.kind === 'tab' || c.kind === 'seat';
          const vis = onScreen(p, cam, vp);
          // OCCLUSION: off-screen cards aren't mounted — except viewports in the
          // live set (hero + recent), whose stream state must survive a pan.
          if (!vis && !(isVp && liveSet.has(c.key))) { ghosts++; return <div key={c.key} className="card-ghost" style={{ left: p.x, top: p.y, width: p.w, height: p.h }} />; }
          mounted++;
          // picture cards (viewports, live desktops) stay pictures when small — a
          // thumbnail of the real desktop reads; a 40px label of it does not
          const lod = !isVp && !c.aspect && p.w * cam.z < 150;
          let body = c.node;
          if (isVp) {
            const seatId = c.kind === 'seat' ? c.key : c.lane;
            const seat = seatOfLane(c.lane);
            const s = seat?.id || seatId;
            const tab = c.kind === 'tab' ? (tabsBy[s] || []).find((t) => s + (t.context || '') === c.key) : undefined;
            const hero = c.key === heroKey;
            const isFox = !!seat && seat.physics === 'channel' && seat.stream !== 'cdp' && seat.stream !== 'text' && !!tab?.context;
            const inSet = liveSet.has(c.key);
            const live = isFox ? inSet : (p.top && vis);
            const isParked = tab?.parked === 'true';
            const isText = seat?.stream === 'text';
            const streams = !isParked && (isText ? true : (isFox ? inSet : p.top)) && vis;
            body = <Viewport session={s} context={tab?.context} title={c.title} url={tab?.url}
              parked={isParked} visible={streams} live={!isParked && live}
              fps={parentMem > 3500 ? (hero ? 1 : 0) : (hero ? 3 : (streams ? 0.4 : 0))} pinned={hero}
              lodW={lodBucket(p.w * cam.z)}
              fx={isFox} fxNeedle={tab?.url ? hostOf(tab.url) : ''}
              onPin={() => setPinnedKey(c.key)}
              onAspect={(r) => setAspectBy((prev) => (Math.abs((prev[c.key] || 0) - r) > 0.01 ? { ...prev, [c.key]: r } : prev))}
              hud={tab?.url ? hudBy[tab.url] : undefined} />;
          }
          return (
            <CardFrame key={c.key} card={c} rect={p} lod={lod}
              onHide={isVp ? undefined : () => setHidden((h) => ({ ...h, [c.key]: true }))}
              onEnter={isVp ? () => setHoverKey(c.key) : undefined}
              onLeave={isVp ? () => setHoverKey((h) => (h === c.key ? '' : h)) : undefined}>
              {body}
            </CardFrame>
          );
        }))}
      </div>
      <div className="rec-bar">
        <button className={`rec-btn${rec.recording ? ' on' : ''}`} onClick={toggleRec} title="record every /act you drive on a live card; replay re-fires them — deterministic">
          {rec.recording ? `● REC ${rec.name} · ${rec.frames ?? 0} cmds` : '○ record'}
        </button>
        {!rec.recording && <input className="rec-in" value={recName} onChange={(e) => setRecName(e.target.value)} title="case name" />}
        {series.length > 0 && (
          <select className="rec-in" value="" onChange={(e) => { if (e.target.value) replaySeries(e.target.value); e.currentTarget.value = ''; }} title="replay a saved case">
            <option value="">▶ replay…</option>
            {series.map((s) => <option key={s.name} value={s.name}>{s.name} · {s.frames} cmds</option>)}
          </select>
        )}
      </div>
      <div className="persp-bar">
        <button onClick={persp.p1} title="one card">P1 · act</button>
        <button onClick={persp.p2} title="all lanes, side by side">P2 · lanes</button>
        <button onClick={persp.bird} title="see everything">◇ bird's-eye</button>
        <button className="lvl" onClick={cycleLevel} title="semantic zoom: hosts → profiles → tabs (auto follows the zoom)">◎ {levelPick === 'auto' ? `auto · ${level}` : level}</button>
        <button className={showSelf ? 'on' : ''} onClick={() => setShowSelf((v) => !v)} title="reflexive: let this 8 see its OWN tab (1 tab → 2 panes → controls itself)">⟲ self</button>
        <button className={theater ? 'on' : ''} onClick={() => setTheater((v) => !v)} title="watch the pinned card at the real tab's full size (1:1) — stay on 8, see the action live">⛶ watch</button>
        <button onClick={() => showCard('panes')} title="broadcast one prompt to selected/all live claude panes (POST /panes/send)">📣 send</button>
        <button onClick={() => showCard('heart')} title="one pane as a pod: measured context per turn (usage), process, inspector">🫀 live</button>
        <button className={menu ? 'on' : ''} onClick={() => setMenu((v) => !v)} title="every card kind: show / hide / go to">▤ cards</button>
        <button onClick={() => { setPosBy({}); setHidden({}); ledger.reset(); resetDrag(); location.reload(); }} title="forget operator positions, hidden cards and arrival order — return to the deterministic layout">⌂ layout</button>
        <span className="persp-z">{ordered.length} lanes · {allCards.length} cards · {mounted} live · {ghosts} occluded · {Math.round(cam.z * 100)}%</span>
      </div>
      {menu && (
        <div className="cards-menu" onPointerDown={(e) => e.stopPropagation()}>
          {Object.values(typeCards).map((c) => (
            <button key={c.key} className={isHidden(c.key) ? '' : 'on'} title={isHidden(c.key) ? 'show + go to' : 'go to (✕ on the card hides it)'}
              onClick={() => showCard(c.key)}>{isHidden(c.key) ? '○' : '●'} {c.title}</button>
          ))}
        </div>
      )}
      {theater && (() => {
        // THEATER — the watched card at the real tab's full size (1:1).
        const h = tabCards.find((c) => c.key === heroKey) || tabCards[0];
        if (!h) return null;
        const seat = seatOfLane(h.lane); const s = seat?.id || h.key;
        const tab = (tabsBy[s] || []).find((t) => s + (t.context || '') === h.key);
        const vw = vp.w || 1200, vh = vp.h || 800;
        const aspect = aspectBy[h.key] || 1.6;
        let W = vw - 48, H2 = W / aspect;
        if (H2 > vh - 96) { H2 = vh - 96; W = H2 * aspect; }
        return (
          <div className="theater" onPointerDown={(e) => { if (e.target === e.currentTarget) setTheater(false); }}>
            <div className="theater-frame" style={{ width: Math.round(W), height: Math.round(H2) }}>
              <Viewport session={s} context={tab?.context} url={tab?.url} title={h.title}
                visible live fps={12} lodW={Math.round(W)} hiRes
                fx={!!seat && seat.stream !== 'cdp' && seat.stream !== 'text' && !!tab?.context} fxNeedle={tab?.url || ''} />
            </div>
            <button className="theater-exit" onClick={() => setTheater(false)} title="exit theater (Esc)">✕ exit</button>
          </div>
        );
      })()}
      {vp.w > 0 && world.w > 0 && (
        <svg className="minimap" viewBox={`0 0 ${world.w} ${world.h}`}
          style={{ width: 220, height: Math.round(Math.min(170, 220 * world.h / world.w)) }}
          onPointerDown={(e) => {
            // DRAG to scrub the camera across the WHOLE world: the cursor on the
            // minimap IS where the view centers. (One rect read of the minimap on
            // click — a hit-test, not a layout measurement of any card.)
            const svg = e.currentTarget as SVGSVGElement;
            const r = svg.getBoundingClientRect();
            const to = (cx: number, cy: number) => {
              const wx = (cx - r.left) / r.width * world.w, wy = (cy - r.top) / r.height * world.h;
              setCam((c) => ({ ...c, x: vp.w / 2 - wx * c.z, y: vp.h / 2 - wy * c.z }));
            };
            to(e.clientX, e.clientY);
            const mv = (ev: PointerEvent) => to(ev.clientX, ev.clientY);
            const up = () => { window.removeEventListener('pointermove', mv); window.removeEventListener('pointerup', up); };
            window.addEventListener('pointermove', mv); window.addEventListener('pointerup', up);
          }}>
          <rect x={0} y={0} width={world.w} height={world.h} className="mm-bg" />
          {world.lanes.flatMap((L) => L.cards.map((p) => (
            <rect key={p.key} x={p.x} y={p.y} width={p.w} height={p.h} className={p.key === heroKey ? 'mm-hero' : 'mm-seat'} />
          )))}
          {(() => {
            const vx = Math.max(0, -cam.x / cam.z), vy = Math.max(0, -cam.y / cam.z);
            const vx2 = Math.min(world.w, (vp.w - cam.x) / cam.z), vy2 = Math.min(world.h, (vp.h - cam.y) / cam.z);
            return <rect x={vx} y={vy} width={Math.max(0, vx2 - vx)} height={Math.max(0, vy2 - vy)} className="mm-view" vectorEffect="non-scaling-stroke" />;
          })()}
        </svg>
      )}
    </div>
  );
}

// ── the recording deck body + its text ───────────────────────────────────────
function recText(rec: { recording: boolean; name?: string; frames?: number; captured?: CapFrame[] }): string {
  const rows = rec.captured || [];
  if (!rows.length) return rec.recording ? 'drive a live card — each command lands here as you go' : 'press ● record, then drive a card; the case builds here';
  return rows.map((f) => `${f.seq} ${f.physics === 'channel' ? '⟂' : '→'} ${f.method} ${hostOf(f.url)} ${f.seat || ''} ${f.status || '·'}`).join('\n');
}
function RecBody({ rec }: { rec: { recording: boolean; name?: string; frames?: number; captured?: CapFrame[] } }) {
  return (
    <div className="rec-deck-body">
      {!(rec.captured && rec.captured.length) && <div className="empty">{rec.recording ? 'drive a live card — each command lands here as you go' : 'press ● record, then drive a card; the case builds here'}</div>}
      {(rec.captured || []).slice().reverse().map((f) => (
        <div key={f.seq} className={`cap-card ${f.physics}`} title={f.url}>
          <span className="cap-seq">{f.seq}</span>
          <span className={`cap-phys ${f.physics}`}>{f.physics === 'channel' ? '⟂' : '→'}</span>
          <span className="cap-method">{f.method}</span>
          <span className="cap-url">{hostOf(f.url)}</span>
          {f.seat && <span className="cap-seat">{f.seat}</span>}
          <span className={`cap-status s${Math.floor((f.status || 0) / 100)}`}>{f.status || '·'}</span>
        </div>
      ))}
    </div>
  );
}
