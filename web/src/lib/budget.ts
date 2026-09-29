export type BudgetWindow = { utilization: number; status?: string; reset_in_s?: number };
export type BudgetProvider = {
  phase?: string; research_ok?: boolean; research_ceil?: number; cap?: number; five_h_util?: number;
  windows: Record<string, BudgetWindow | null>; gated?: boolean; observed_at?: string;
};
export type BudgetResponse = {
  budget?: BudgetProvider | null;
  providers?: { claude: BudgetProvider | null; codex: BudgetProvider | null };
  observed_age_s?: number | null; codex_observed_age_s?: number | null; sensors_armed?: number;
  refreshed?: { claude: boolean; codex: boolean }; reasons?: Record<string, string>;
};
export type BudgetPeer = {
  host: string; age_s: number; stale: boolean;
  addr?: string; // the peer's collector base URL (from /peers), for a cross-host budget poke
  extra?: { budget?: Record<string, number | boolean | string | null> };
};
export type BudgetHost = { host: string; os?: string };
export type ProviderName = 'claude' | 'codex';

export function localBudgetHost(host: BudgetHost | null): string {
  return host?.os === 'darwin' ? 'mac' : host?.host || 'local';
}

// A heartbeat's age is NOT the provider reading's age. Old peer payloads without
// observed_at remain last-known even when their heartbeat is fresh.
export function observedAge(at?: string, now = Date.now()): number | null {
  const ts = at ? Date.parse(at) : NaN;
  return Number.isFinite(ts) ? Math.max(0, Math.floor((now - ts) / 1000)) : null;
}

export function peerProvider(peer: BudgetPeer, provider: ProviderName): BudgetProvider | null {
  const budget = peer.extra?.budget;
  if (!budget) return null;
  const value = (window: string) => {
    const n = budget[`${provider}_${window}`];
    return typeof n === 'number' && Number.isFinite(n) && n >= 0 ? n : null;
  };
  const five = value('5h'), seven = value('7d');
  if (five == null && seven == null) return null;
  const gated = budget[`${provider}_gated`] === true || (five ?? 0) >= .95 || (seven ?? 0) >= .95;
  const observed = budget[`${provider}_observed_at`];
  return {
    windows: { '5h': five == null ? null : { utilization: five }, '7d': seven == null ? null : { utilization: seven } },
    five_h_util: five ?? undefined, gated,
    phase: gated ? 'gated' : five != null && five < .5 ? 'research' : 'conserve',
    observed_at: typeof observed === 'string' ? observed : undefined,
  };
}

export function remoteBudgetPeers(peers: BudgetPeer[], host: BudgetHost | null): BudgetPeer[] {
  const local = new Set([host?.host.toLowerCase(), localBudgetHost(host).toLowerCase()]);
  return peers.filter((peer) => !local.has(peer.host.toLowerCase()))
    .sort((a, b) => a.host.localeCompare(b.host));
}
