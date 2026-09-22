import { useEffect, useState } from 'react';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// BUDGET HUD — always visible in the statusline. TWO budget families, clearly
// segregated: CLAUDE (anthropic-ratelimit-unified-* headers observed by a tapped
// pane) and CODEX (rollout rate_limits via codex-budget.sh). Each segment shows
// its own phase (research=work hard / conserve=hold heavy work / gated=hard cap),
// the 5h bar with the research ceiling (50%) and hard cap (95%) marked, 5h/7d,
// and how fresh the reading is. ⟳ renews BOTH via POST /budget/poke?provider=both
// (each provider's refresh is an OBSERVED response; refreshed:false shows why).
type Win = { utilization: number; status?: string; reset_in_s?: number };
type Prov = { phase: string; research_ok: boolean; research_ceil?: number; cap?: number; five_h_util?: number;
  windows: Record<string, Win | null>; gated: boolean };
type Resp = {
  budget?: Prov | null; // legacy key (claude)
  providers?: { claude: Prov | null; codex: Prov | null };
  observed_age_s?: number | null; codex_observed_age_s?: number | null; sensors_armed?: number;
  refreshed?: { claude: boolean; codex: boolean }; reasons?: Record<string, string>; note?: string;
};
type Renew = { at: number; refreshed: { claude: boolean; codex: boolean }; reasons: Record<string, string> };

const hhm = (s?: number | null) => s == null ? '' : s > 3600 ? `${Math.floor(s / 3600)}h${Math.round((s % 3600) / 60)}m` : `${Math.round(s / 60)}m`;
const ago = (s: number) => s < 60 ? `${s}s` : hhm(s);
const pct = (u?: number | null) => `${((u ?? 0) * 100).toFixed(0)}%`;

// One provider's reading. phase: the provider's own; gated overrides (red).
function Segment({ label, p, age, why }: { label: string; p: Prov | null; age?: number | null; why?: string }) {
  if (!p || !p.windows) {
    return (
      <span className="hud-seg hud-seg-none" title={why || `${label}: no reading yet`}>
        <span className="hud-tag">{label}</span>
        <span className="hud-num dim">· no reading</span>
        {why && <span className="hud-why" title={why}>⚠ {why}</span>}
      </span>
    );
  }
  const five = p.five_h_util ?? p.windows['5h']?.utilization ?? 0;
  const sevenD = p.windows['7d']?.utilization ?? 0;
  const ceil = p.research_ceil ?? 0.5, cap = p.cap ?? 0.95;
  const phase = p.gated ? 'gated' : (p.phase === 'research' ? 'research' : 'conserve');
  const stale = age == null || age > 900;
  const resets = Object.values(p.windows).find((w) => w && (w.status === 'rejected' || (w.utilization ?? 0) >= cap))?.reset_in_s;
  const tip = `${label} · 5h ${pct(five)} · research ceiling ${pct(ceil)} · hard cap ${pct(cap)} · 7d ${pct(sevenD)} · ${age == null ? 'never observed' : `observed ${ago(age)} ago`}${why ? ` · not refreshed: ${why}` : ''}`;
  return (
    <span className={`hud-seg hud-seg-${phase}`} title={tip}>
      <span className="hud-tag">{label}</span>
      <span className={`hud-pill hud-pill-${phase}`}>{phase.toUpperCase()}</span>
      <span className="hud-bar" aria-hidden>
        <span className="hud-fill" style={{ width: `${Math.min(100, five * 100)}%` }} />
        <span className="hud-mark hud-ceil" style={{ left: `${ceil * 100}%` }} />
        <span className="hud-mark hud-cap" style={{ left: `${cap * 100}%` }} />
      </span>
      <span className="hud-num">5h {pct(five)}</span>
      <span className="hud-num dim">7d {pct(sevenD)}</span>
      {phase === 'gated' && resets != null && <span className="hud-gated">⏸ resets {hhm(resets)}</span>}
      {stale
        ? <span className="hud-stale" title="no recent observed response — last-known">·stale{age != null ? ` ${hhm(age)}` : ''}</span>
        : <span className="hud-obs">observed {ago(age as number)} ago</span>}
      {why && <span className="hud-why" title={why}>⚠ {why}</span>}
    </span>
  );
}

export function BudgetHud() {
  const [r, setR] = useState<Resp | null>(null);
  const [poking, setPoking] = useState(false);
  const [renew, setRenew] = useState<Renew | null>(null);
  const poke = async () => {
    if (poking) return;
    setPoking(true);
    try {
      const j: Resp = await (await fetch(`${BASE}/budget/poke?provider=both`, { method: 'POST' })).json();
      // update BOTH segments from the returned providers (legacy `budget` kept in sync)
      setR((p) => ({ ...(p || {}), ...j, budget: j.providers?.claude ?? j.budget ?? null }));
      setRenew({ at: Date.now(), refreshed: j.refreshed ?? { claude: false, codex: false }, reasons: j.reasons ?? {} });
    } catch (e) {
      setRenew({ at: Date.now(), refreshed: { claude: false, codex: false }, reasons: { claude: `poke failed: ${e}`, codex: `poke failed: ${e}` } });
    }
    setPoking(false);
  };
  useEffect(() => {
    let dead = false;
    const tick = () => fetch(`${BASE}/budget`).then((x) => x.json()).then((j) => { if (!dead) setR(j); }).catch(() => {});
    tick(); const t = setInterval(tick, 4000);
    return () => { dead = true; clearInterval(t); };
  }, []);
  const claude = r?.providers?.claude ?? r?.budget ?? null; // back-compat when providers is absent
  const codex = r?.providers ? r.providers.codex : null;
  const whyOf = (k: 'claude' | 'codex') => renew && !renew.refreshed[k] ? (renew.reasons[k] || 'not refreshed') : undefined;
  return (
    <span className="hud hud-dual" title={`budget families, segregated · ${r?.sensors_armed ?? 0} claude sensors armed`}>
      <Segment label="CLAUDE" p={claude} age={r?.observed_age_s} why={whyOf('claude')} />
      <span className="hud-div" aria-hidden />
      <Segment label="CODEX" p={codex} age={r?.codex_observed_age_s} why={whyOf('codex')} />
      <button className="hud-refresh" disabled={poking}
        title="renew BOTH readings: a tapped trivial claude call + codex-budget.sh --poke, each an observed response"
        onClick={(e) => { e.stopPropagation(); poke(); }}>
        <span className={poking ? 'hud-spin' : ''}>⟳</span>
      </button>
    </span>
  );
}
