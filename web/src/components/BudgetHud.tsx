import { useEffect, useState } from 'react';
import { localBudgetHost, observedAge, peerProvider, remoteBudgetPeers, type BudgetHost, type BudgetPeer, type BudgetProvider, type BudgetResponse, type ProviderName } from '../lib/budget';
import './BudgetHud.css';

const BASE = import.meta.env.VITE_COLLECTOR_URL || 'http://127.0.0.1:7070';

// Local provider responses and peer heartbeat snapshots stay separately named.
// Refresh requests apply only to the local collector; a heartbeat is never
// mistaken for a fresh provider observation.
type Renew = { at: number; refreshed: { claude: boolean; codex: boolean }; reasons: Record<string, string> };

const hhm = (s?: number | null) => s == null ? '' : s > 3600 ? `${Math.floor(s / 3600)}h${Math.round((s % 3600) / 60)}m` : `${Math.round(s / 60)}m`;
const ago = (s: number) => s < 60 ? `${s}s` : hhm(s);
const pct = (u?: number | null) => u == null ? '—' : `${(u * 100).toFixed(0)}%`;

// One provider's reading. phase: the provider's own; gated overrides (red).
function Segment({ label, p, age, why }: { label: string; p: BudgetProvider | null; age?: number | null; why?: string }) {
  if (!p || !p.windows) {
    return (
      <span className="hud-seg hud-seg-none" title={why || `${label}: no reading yet`}>
        <span className="hud-tag">{label}</span>
        <span className="hud-num dim">· no reading</span>
        {why && <span className="hud-why" title={why}>⚠ {why}</span>}
      </span>
    );
  }
  const five = p.five_h_util ?? p.windows['5h']?.utilization;
  const sevenD = p.windows['7d']?.utilization;
  const fiveReset = p.windows['5h']?.reset_in_s, sevenReset = p.windows['7d']?.reset_in_s;
  const ceil = p.research_ceil ?? 0.5, cap = p.cap ?? 0.95;
  const phase = p.gated ? 'gated' : (p.phase === 'research' ? 'research' : 'conserve');
  const stale = age == null || age > 900;
  const resets = Object.values(p.windows).find((w) => w && (w.status === 'rejected' || (w.utilization ?? 0) >= cap))?.reset_in_s;
  const tip = `${label} · ${phase} · 5h ${pct(five)} · research ceiling ${pct(ceil)} · hard cap ${pct(cap)} · 7d ${pct(sevenD)} · ${age == null ? 'observation time unavailable — last-known' : `observed ${ago(age)} ago`}${resets != null ? ` · resets ${hhm(resets)}` : ''}${why ? ` · not refreshed: ${why}` : ''}`;
  return (
    <span className={`hud-seg hud-seg-${phase}`} title={tip}>
      <span className="hud-tag">{label}</span>
      <span className="hud-phase" aria-label={phase} title={phase}>●</span>
      <span className="hud-num">5h {pct(five)}</span>
      {fiveReset != null
        ? <span className="hud-reset" title={`5h window resets in ${hhm(fiveReset)} — plan ahead`}>↻{hhm(fiveReset)}</span>
        : p.pane_reset && <span className="hud-reset" title={`resets ${p.pane_reset} (from the pane)`}>↻{p.pane_reset}</span>}
      <span className="hud-num dim">7d {pct(sevenD)}</span>
      {sevenReset != null && <span className="hud-reset dim" title={`7d window resets in ${hhm(sevenReset)}`}>↻{hhm(sevenReset)}</span>}
      {stale
        ? <span className="hud-stale" title="no recent observed response — last-known">stale{age != null ? ` ${hhm(age)}` : ''}</span>
        : <span className="hud-obs" title={`observed ${ago(age as number)} ago`}>{ago(age as number)} ago</span>}
      {why && <span className="hud-why" title={why}>⚠ {why}</span>}
    </span>
  );
}

export function BudgetHud() {
  const [r, setR] = useState<BudgetResponse | null>(null);
  const [host, setHost] = useState<BudgetHost | null>(null);
  const [peers, setPeers] = useState<BudgetPeer[]>([]);
  const [errors, setErrors] = useState<Record<string, boolean>>({});
  const [now, setNow] = useState(Date.now());
  const [budgetAt, setBudgetAt] = useState(Date.now());
  const [poking, setPoking] = useState(false);
  const [renew, setRenew] = useState<Renew | null>(null);
  const poke = async () => {
    if (poking) return;
    setPoking(true);
    try {
      const response = await fetch(`${BASE}/budget/poke?provider=both`, { method: 'POST' });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const j: BudgetResponse = await response.json();
      // update BOTH segments from the returned providers (legacy `budget` kept in sync)
      setR((p) => ({ ...(p || {}), ...j, budget: j.providers?.claude ?? j.budget ?? null }));
      setBudgetAt(Date.now());
      setRenew({ at: Date.now(), refreshed: j.refreshed ?? { claude: false, codex: false }, reasons: j.reasons ?? {} });
    } catch (e) {
      setRenew({ at: Date.now(), refreshed: { claude: false, codex: false }, reasons: { claude: `poke failed: ${e}`, codex: `poke failed: ${e}` } });
    }
    setPoking(false);
  };
  // PER-PEER refresh: poke the PEER's own collector over Tailscale (collectors send
  // Access-Control-Allow-Origin:*, so the cross-origin POST is allowed) so it re-taps
  // its budget; the fresh reading arrives on that peer's next heartbeat, not this
  // response. Answers "no refresh button for other systems' usages."
  const [pokingPeer, setPokingPeer] = useState('');
  const pokePeer = async (peer: BudgetPeer) => {
    if (!peer.addr || pokingPeer) return;
    setPokingPeer(peer.host);
    try { await fetch(`${peer.addr}/budget/poke?provider=both`, { method: 'POST' }); }
    catch { /* refreshed reading lands via heartbeat, not here */ }
    setPokingPeer('');
  };
  useEffect(() => {
    let dead = false;
    const controller = new AbortController();
    const get = async (path: string) => {
      const response = await fetch(`${BASE}/${path}`, { signal: controller.signal });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      return response.json();
    };
    get('hostres').then((j) => { if (!dead) setHost(j); }).catch(() => {});
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      await Promise.all(['budget', 'peers'].map(async (path) => {
        try {
          const j = await get(path);
          if (dead) return;
          if (path === 'budget') { setR(j); setBudgetAt(Date.now()); }
          else setPeers(j.peers ?? []);
          setErrors((before) => ({ ...before, [path]: false }));
        } catch {
          if (!dead) setErrors((before) => ({ ...before, [path]: true }));
        }
      }));
      if (!dead) timer = setTimeout(tick, 4000);
    };
    tick();
    const clock = setInterval(() => setNow(Date.now()), 1000);
    return () => { dead = true; controller.abort(); clearTimeout(timer); clearInterval(clock); };
  }, []);
  const claude = r?.providers?.claude ?? r?.budget ?? null; // back-compat when providers is absent
  const codex = r?.providers ? r.providers.codex : null;
  const whyOf = (k: ProviderName) => renew && !renew.refreshed[k] ? (renew.reasons[k] || 'not refreshed') : undefined;
  const localAge = (p: BudgetProvider | null, age?: number | null) => observedAge(p?.observed_at, now) ?? (age == null ? null : age + Math.max(0, Math.floor((now - budgetAt) / 1000)));
  const hostName = localBudgetHost(host);
  return (
    <span className="hud hud-dual hud-fleet" aria-label="Budgets by host">
      <span className="hud-host" data-budget-host={hostName}>
        <span className="hud-host-head" title={`${host?.host ?? 'local collector'} · /budget · ${r?.sensors_armed ?? 0} Claude sensors armed`}>
          <b>{hostName}</b><span className="hud-host-source">local</span>
          {errors.budget && <span className="hud-stale">budget unavailable</span>}
          <button className="hud-refresh" disabled={poking} aria-label={`Refresh ${hostName} budgets`}
            title={`renew ${hostName} Claude + Codex only; peer readings arrive by heartbeat`}
            onClick={(e) => { e.stopPropagation(); poke(); }}>
            <span className={poking ? 'hud-spin' : ''}>⟳</span>
          </button>
        </span>
        <Segment label="CLAUDE" p={claude} age={localAge(claude, r?.observed_age_s)} why={whyOf('claude')} />
        <Segment label="CODEX" p={codex} age={localAge(codex, r?.codex_observed_age_s)} why={whyOf('codex')} />
      </span>
      {remoteBudgetPeers(peers, host).map((peer) => (
        <span className="hud-host" data-budget-host={peer.host} key={peer.host}>
          <span className="hud-host-head" title={`/peers · ${peer.host} reports this snapshot · heartbeat ${ago(peer.age_s)} ago`}>
            <b>{peer.host}</b><span className="hud-host-source">peer</span>
            {peer.addr && (
              <button className="hud-renew" title={`refresh ${peer.host}'s usage — poke its collector; the fresh reading arrives on its next heartbeat`}
                onClick={(e) => { e.stopPropagation(); pokePeer(peer); }}>
                <span className={pokingPeer === peer.host ? 'hud-spin' : ''}>⟳</span>
              </button>
            )}
            {(peer.stale || errors.peers) && <span className="hud-stale">peer stale</span>}
          </span>
          {(['claude', 'codex'] as const).map((provider) => {
            const reading = peerProvider(peer, provider);
            return <Segment key={provider} label={provider.toUpperCase()} p={reading} age={observedAge(reading?.observed_at, now)} />;
          })}
        </span>
      ))}
      {errors.peers && <span className="hud-stale" role="status">peers unavailable</span>}
    </span>
  );
}
