import type { ReactNode } from 'react';
import type { Card } from '../lib/cards';
import type { HostNode, ProfileNode } from '../lib/nodes';
import { BudgetHud } from './BudgetHud';

// ── CardFrame — the ONE chrome every card wears ───────────────────────────────
// header: [kind] title · meta … [📍 pin] [✕ hide]      body: the card's node
// bare cards (viewports) bring their own header; they get the kind badge overlaid.
// lod: when the card is displayed too small to read (world w × zoom < ~150px)
// the body is replaced by a big label — occlusion at the detail level, not the
// rect level: the card stays, its cost goes.
export function CardFrame({ card, rect, lod, onHide, onPin, onEnter, onLeave, children }: {
  card: Card; rect: { x: number; y: number; w: number; h: number; z: number }; lod: boolean;
  onHide?: () => void; onPin?: () => void; onEnter?: () => void; onLeave?: () => void; children: ReactNode;
}) {
  const bare = !!card.bare;
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
        : <div className={`card-b${bare ? '' : ' scroll'}`}>{children}</div>}
    </div>
  );
}

// ── level bodies: host / profile — and their TEXT (what the engine measures) ──
const short = (u?: string | null) => (u || '').replace(/^https?:\/\//, '').replace(/\/$/, '');
const hostOf = (u: string) => { try { return new URL(u).host.replace(/^www\./, ''); } catch { return u || 'tab'; } };

export function hostLines(h: HostNode): string[] {
  const tabs = h.profiles.reduce((a, p) => a + p.tabs.length, 0);
  const views = h.profiles.reduce((a, p) => a + p.viewTabs.length, 0);
  return [
    h.label, ' ',
    `engine   ${h.engine} · ${h.mode}`,
    `profiles ${h.profiles.map((p) => p.label).join(', ')}`,
    h.node?.view_url ? `view     ${short(h.node.view_url)}` : '',
    h.node?.cdp_url ? `cdp      ${short(h.node.cdp_url)}` : '',
    `seat     ${h.seat ? `${h.seat.id} · ${h.seat.status}${h.seat.stream ? ' · ' + h.seat.stream : ''}` : 'no session yet (tabs come via BiDi)'}`,
    `tabs     ${tabs}${views ? ` · ${views} viewing` : ''}`,
    ...(tabs ? [h.profiles.flatMap((p) => p.tabs).slice(0, 12).map((t) => hostOf(t.url)).join('  ')] : []),
  ].filter(Boolean);
}
export function HostBody({ host, onZoom }: { host: HostNode; onZoom: () => void }) {
  const tabs = host.profiles.flatMap((p) => p.tabs);
  const views = host.profiles.reduce((a, p) => a + p.viewTabs.length, 0);
  return (
    <div className="card-host" onClick={onZoom} title="zoom in → profiles">
      <div className={`h-big ${host.tone}`}>{host.label}</div>
      <div className="h-line"><span className="h-k">engine</span><span className="h-v">{host.engine} · {host.mode}</span></div>
      <div className="h-line"><span className="h-k">profiles</span><span className="h-v">{host.profiles.map((p) => p.label).join(', ')}</span></div>
      {host.node?.view_url && <div className="h-line"><span className="h-k">view</span><span className="h-v dim">{short(host.node.view_url)}</span></div>}
      {host.node?.cdp_url && <div className="h-line"><span className="h-k">cdp</span><span className="h-v dim">{short(host.node.cdp_url)}</span></div>}
      <div className="h-line"><span className="h-k">seat</span><span className={`h-v${host.seat ? '' : ' dim'}`}>{host.seat ? `${host.seat.id} · ${host.seat.status}${host.seat.stream ? ' · ' + host.seat.stream : ''}` : 'no session yet (tabs come via BiDi)'}</span></div>
      <div className="h-line"><span className="h-k">tabs</span><span className="h-v">{tabs.length}{views ? ` · ${views} viewing` : ''}</span></div>
      {tabs.length > 0 && <div className="h-tabs">{tabs.slice(0, 12).map((t) => hostOf(t.url)).join('  ')}</div>}
    </div>
  );
}
export function profileLines(h: HostNode, p: ProfileNode): string[] {
  return [
    p.label, ' ',
    `host     ${h.label} · ${h.engine}`,
    p.node?.view_url ? `view     ${short(p.node.view_url)}` : '',
    `seat     ${p.seat ? `${p.seat.id} · ${p.seat.status}` : 'no session yet'}`,
    `tabs     ${p.tabs.length}${p.viewTabs.length ? ` · ${p.viewTabs.length} viewing` : ''}`,
    ...p.tabs.slice(0, 16).map((t) => `· ${t.title || hostOf(t.url)}`),
  ].filter(Boolean);
}
export function ProfileBody({ host, profile, onZoom }: { host: HostNode; profile: ProfileNode; onZoom: () => void }) {
  return (
    <div className="card-host" onClick={onZoom} title="zoom in → tabs">
      <div className={`h-big ${host.tone}`}>{profile.label}</div>
      <div className="h-line"><span className="h-k">host</span><span className="h-v">{host.label} · {host.engine}</span></div>
      {profile.node?.view_url && <div className="h-line"><span className="h-k">view</span><span className="h-v dim">{short(profile.node.view_url)}</span></div>}
      <div className="h-line"><span className="h-k">seat</span><span className={`h-v${profile.seat ? '' : ' dim'}`}>{profile.seat ? `${profile.seat.id} · ${profile.seat.status}` : 'no session yet'}</span></div>
      <div className="h-line"><span className="h-k">tabs</span><span className="h-v">{profile.tabs.length}{profile.viewTabs.length ? ` · ${profile.viewTabs.length} viewing` : ''}</span></div>
      {profile.tabs.slice(0, 16).map((t) => <div key={t.context} className="h-line"><span className="h-k">·</span><span className="h-v dim" title={t.url}>{t.title || hostOf(t.url)}</span></div>)}
    </div>
  );
}

// ── budget card body: the same HUD the statusline shows, as a card ────────────
export const BUDGET_LINES = ['CLAUDE ▮▮▮▮▮▮▮▮▮▮ 5h · 7d', 'CODEX  ▮▮▮▮▮▮▮▮▮▮ 5h · 7d', '⟳ renew both readings'];
export function BudgetBody() {
  return <div className="card-budget"><BudgetHud /></div>;
}
