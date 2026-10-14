import { describe, expect, it } from 'vitest';
import { localBudgetHost, observedAge, peerProvider, remoteBudgetPeers, type BudgetPeer } from './budget';

const peer = (budget: BudgetPeer['extra'] = {}): BudgetPeer => ({ host: 'omarchy', age_s: 1, stale: false, extra: budget });

describe('federated budget readings', () => {
  it('preserves zero usage and unknown windows/providers independently', () => {
    const snapshot = peer({ budget: { claude_5h: 0, claude_7d: null, codex_5h: null, codex_7d: null } });
    expect(peerProvider(snapshot, 'claude')?.windows).toEqual({ '5h': { utilization: 0 }, '7d': null });
    expect(peerProvider(snapshot, 'codex')).toBeNull();
  });
  it('marks weekly saturation gated even if five-hour usage is zero', () => {
    expect(peerProvider(peer({ budget: { claude_5h: 0, claude_7d: 1 } }), 'claude')?.gated).toBe(true);
  });
  it('respects an explicit gate below the normal cap', () => {
    expect(peerProvider(peer({ budget: { codex_5h: .1, codex_gated: true } }), 'codex')?.phase).toBe('gated');
  });
  it('never substitutes a fresh heartbeat for an old or absent observation', () => {
    const snapshot = peer({ budget: { claude_5h: .23, claude_observed_at: '2026-09-28T18:00:00Z' } });
    expect(observedAge(peerProvider(snapshot, 'claude')?.observed_at, Date.parse('2026-09-29T18:00:00Z'))).toBe(86400);
    expect(observedAge(peerProvider(peer({ budget: { codex_5h: .16 } }), 'codex')?.observed_at)).toBeNull();
  });
  it('renders peers without sensors and excludes only the local host aliases', () => {
    const peers = [peer(), { ...peer(), host: 'colima-runtime' }, { ...peer(), host: 'mac' }, { ...peer(), host: 'admin-MacBook-Pro' }];
    expect(remoteBudgetPeers(peers, { host: 'admin-MacBook-Pro', os: 'darwin' }).map((p) => p.host)).toEqual(['colima-runtime', 'omarchy']);
    expect(peerProvider(peers[1], 'claude')).toBeNull();
  });
  it('does not label a non-Mac local collector as mac', () => {
    expect(localBudgetHost({ host: 'omarchy', os: 'linux' })).toBe('omarchy');
    expect(localBudgetHost(null)).toBe('local');
  });
});
