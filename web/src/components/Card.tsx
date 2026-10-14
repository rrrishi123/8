import { useEffect, useRef, useState, type ReactNode } from 'react';
import type { Card } from '../lib/cards';
import { liveView, attachNode, type HostNode, type ProfileNode, type BrowserNode } from '../lib/nodes';
import { reportHeight } from '../lib/cardText';
import { BudgetHud } from './BudgetHud';

// ── CardFrame — the ONE chrome every card wears ───────────────────────────────
// header: [kind] title · meta … [📍 pin] [✕ hide]      body: the card's node
// bare cards (viewports) bring their own header; they get the kind badge overlaid.
// lod: when the card is displayed too small to read (world w × zoom < ~150px)
// the body is replaced by a big label — occlusion at the detail level, not the
// rect level: the card stays, its cost goes.
// measure='dom' (#1147, the third regime): a STRUCTURED body is wrapped and
// measured ONCE by a ResizeObserver; its height goes to the layout store the
// engine reads (cardText.reportHeight). The wrapper is in normal flow inside
// the scrolling body, so its height is the content's own — never the card's —
// and there is no feedback loop. World px == CSS px here (the canvas transform
// does not touch layout boxes). Nothing else in a card measures itself.
function MeasuredBody({ cardKey, children }: { cardKey: string; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current; if (!el) return;
    const ro = new ResizeObserver((es) => { const b = es[0]?.borderBoxSize?.[0]; reportHeight(cardKey, b ? b.blockSize : el.offsetHeight); });
    ro.observe(el);
    return () => ro.disconnect();
  }, [cardKey]);
  return <div className="card-measure" ref={ref}>{children}</div>;
}
export function CardFrame({ card, rect, lod, onHide, onPin, onEnter, onLeave, children }: {
  card: Card; rect: { x: number; y: number; w: number; h: number; z: number }; lod: boolean;
  onHide?: () => void; onPin?: () => void; onEnter?: () => void; onLeave?: () => void; children: ReactNode;
}) {
  const bare = !!card.bare;
  const body = card.measure === 'dom' ? <MeasuredBody cardKey={card.key}>{children}</MeasuredBody> : children;
  return (
    <div className={`card kind-${card.kind}${card.hero ? ' hero' : ''}${bare ? ' bare' : ''}`}
      style={{ left: rect.x, top: rect.y, width: rect.w, height: rect.h, zIndex: rect.z }}
      onPointerEnter={onEnter} onPointerLeave={onLeave}>
      {bare
        ? <span className="card-badge">{card.kind}</span>
        : (
          <div className="card-h" title={`${card.kind} · ${card.title}${card.meta ? ' · ' + card.meta : ''}`}>
            <span className="card-kind">{card.kind}</span>
            <span className="card-title">{card.title}</span>
            {card.meta && <span className="card-meta">{card.meta}</span>}
            <span className="card-acts">
              {onPin && <button title={card.hero ? 'pinned as hero' : 'pin as hero'} onClick={onPin}>{card.hero ? '📌' : '📍'}</button>}
              {onHide && <button title="hide this card (the ▤ cards menu brings it back)" onClick={onHide}>✕</button>}
            </span>
          </div>
        )}
      {lod
        ? <div className="card-lod"><div className="lod-title">{card.title}</div>{card.meta && <div className="lod-meta">{card.meta}</div>}</div>
        : <div className={`card-b${bare ? '' : ' scroll'}`}>{body}</div>}
    </div>
  );
}

// ── level bodies: host / profile — STRUCTURED cards, sized by the dom regime ──
// (no plaintext twin of these rows: CardFrame measures the real body once)
const short = (u?: string | null) => (u || '').replace(/^https?:\/\//, '').replace(/\/$/, '');
const hostOf = (u: string) => { try { return new URL(u).host.replace(/^www\./, ''); } catch { return u || 'tab'; } };

// ── Attach — an UNJOINED node's verb (#1147): hold its browser. The collector
// resolves the node's cdp_url to the browser's socket, starts a channel broker
// on it, and the seat (tabs, /act, screencast) appears on the next poll. An
// unjoined card never zooms you OUT any more; it offers this instead.
export function AttachButton({ node, className = '' }: { node: BrowserNode; className?: string }) {
  const [state, setState] = useState<{ busy: boolean; msg: string; ok?: boolean }>({ busy: false, msg: '' });
  const go = async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (state.busy) return;
    setState({ busy: true, msg: 'attaching…' });
    const r = await attachNode(node.id);
    setState({ busy: false, msg: r.msg, ok: r.ok });
  };
  return (
    <span className={`h-attach ${className}`} onClick={(e) => e.stopPropagation()}>
      <button onClick={go} disabled={state.busy} title={`start a channel broker on ${short(node.cdp_url)} and hold this browser as a seat`}>{state.busy ? '⏳ attaching' : '⚡ attach'}</button>
      {state.msg && <span className={`h-attach-msg${state.ok === false ? ' err' : ''}`} title={state.msg}>{state.msg}</span>}
    </span>
  );
}
const seatCell = (seat: { id: string; status: string; stream?: string } | undefined, node: BrowserNode | undefined, noun: string) => {
  if (seat) return <span className="h-v">{seat.id} · {seat.status}{seat.stream ? ' · ' + seat.stream : ''}</span>;
  if (node?.attachable) return <span className="h-v dim">no session · <AttachButton node={node} /></span>;
  return <span className="h-v dim">{noun}</span>;
};
// ── NodeLive — the container's REAL desktop (Selkies), embedded live ─────────
// The card is a picture card (aspect-sized by cardSize, like a tab viewport):
// the iframe fills the body; the metadata rides a thin strip over its foot.
// Default is WATCH: the iframe takes no pointer events, so a click on the card
// DRILLS INTO the node (onDrill) and a drag pans the canvas, exactly like every
// other card. ✋ drive arms the iframe: pointer events reach the desktop.
// Not gated on a seat — the container is up whether or not a session exists.
export function NodeLive({ url, label, tone, info, onDrill, node }: { url: string; label: string; tone: string; info: string; onDrill: () => void; node?: BrowserNode }) {
  const [armed, setArmed] = useState(false);
  return (
    <div className={`node-live${armed ? ' armed' : ''}`}>
      <iframe src={url} title={`${label} · live desktop`} allow="clipboard-read; clipboard-write; autoplay" />
      {!armed && <div className="nl-catch" onClick={onDrill} title="drill in → this node's live view + tabs" />}
      <div className="nl-bar" onClick={(e) => e.stopPropagation()}>
        <span className={`nl-name ${tone}`}>{label}</span>
        <span className="nl-dot">● live</span>
        <span className="nl-info" title={info}>{info}</span>
        {node?.attachable && <AttachButton node={node} className="nl-attach" />}
        <span className="nl-url" title={url}>{short(url)}</span>
        <button className={armed ? 'on' : ''} onClick={() => setArmed((v) => !v)}
          title={armed ? 'driving the desktop: clicks/keys go to it (click to go back to watching)' : 'arm: drive the desktop directly (clicks/keys/scroll go to it)'}>
          {armed ? '✋ drive' : '👁 watch'}</button>
      </div>
    </div>
  );
}
const seatInfo = (seat?: { id: string; status: string; stream?: string }, tabs = 0) =>
  `${seat ? `seat ${seat.id.slice(0, 8)} · ${seat.status}` : 'no session (desktop up)'} · ${tabs} tab${tabs === 1 ? '' : 's'}`;

export function HostBody({ host, onZoom }: { host: HostNode; onZoom: () => void }) {
  const tabs = host.profiles.flatMap((p) => p.tabs);
  const views = host.profiles.reduce((a, p) => a + p.viewTabs.length, 0);
  const live = liveView(host.node);
  if (live) return <NodeLive url={live} label={host.label} tone={host.tone} info={`${host.engine} · ${host.mode} · ${seatInfo(host.seat, tabs.length)}`} onDrill={onZoom} node={host.node} />;
  return (
    <div className="card-host" onClick={onZoom} title="drill in → this host">
      <div className={`h-big ${host.tone}`}>{host.label}</div>
      <div className="h-line"><span className="h-k">engine</span><span className="h-v">{host.engine} · {host.mode}</span></div>
      <div className="h-line"><span className="h-k">profiles</span><span className="h-v">{host.profiles.map((p) => p.label).join(', ')}</span></div>
      {host.node?.view_url && <div className="h-line"><span className="h-k">view</span><span className="h-v dim">{short(host.node.view_url)}</span></div>}
      {host.node?.cdp_url && <div className="h-line"><span className="h-k">cdp</span><span className="h-v dim">{short(host.node.cdp_url)}</span></div>}
      <div className="h-line"><span className="h-k">seat</span>{seatCell(host.seat, host.node, 'no session yet (tabs come via BiDi)')}</div>
      <div className="h-line"><span className="h-k">tabs</span><span className="h-v">{tabs.length}{views ? ` · ${views} viewing` : ''}</span></div>
      {tabs.length > 0 && <div className="h-tabs">{tabs.slice(0, 12).map((t) => hostOf(t.url)).join('  ')}</div>}
    </div>
  );
}
export function ProfileBody({ host, profile, onZoom }: { host: HostNode; profile: ProfileNode; onZoom: () => void }) {
  const live = liveView(profile.node);
  if (live) return <NodeLive url={live} label={`${host.label} · ${profile.label}`} tone={host.tone} info={`${host.engine} · ${seatInfo(profile.seat, profile.tabs.length)}`} onDrill={onZoom} node={profile.node} />;
  return (
    <div className="card-host" onClick={onZoom} title="drill in → this profile's tabs">
      <div className={`h-big ${host.tone}`}>{profile.label}</div>
      <div className="h-line"><span className="h-k">host</span><span className="h-v">{host.label} · {host.engine}</span></div>
      {profile.node?.view_url && <div className="h-line"><span className="h-k">view</span><span className="h-v dim">{short(profile.node.view_url)}</span></div>}
      <div className="h-line"><span className="h-k">seat</span>{seatCell(profile.seat, profile.node, 'no session yet')}</div>
      <div className="h-line"><span className="h-k">tabs</span><span className="h-v">{profile.tabs.length}{profile.viewTabs.length ? ` · ${profile.viewTabs.length} viewing` : ''}</span></div>
      {profile.tabs.slice(0, 16).map((t) => <div key={t.context} className="h-line"><span className="h-k">·</span><span className="h-v dim" title={t.url}>{t.title || hostOf(t.url)}</span></div>)}
    </div>
  );
}

// ── budget card body: the same HUD the statusline shows, as a card (dom regime) ─
export function BudgetBody() {
  return <div className="card-budget"><BudgetHud /></div>;
}
