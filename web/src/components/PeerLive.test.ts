import { describe, expect, it } from 'vitest';
import { peerActUrl, peerShotUrl } from './PeerLive';

describe('PeerLive urls (#9970)', () => {
  it('routes pixels through THIS collector\'s /peer proxy, engine-aware', () => {
    expect(peerShotUrl('peer-b', 'chrome', 'AB')).toMatch(/\/peer\/peer-b\/shot\?session=chrome&context=AB&w=480$/);
    expect(peerShotUrl('peer-b', 'fox', '12')).toMatch(/\/peer\/peer-b\/drawshot\?session=fox&context=12&w=480$/);
  });
  it('routes input through the same proxy, never to the peer directly', () => {
    expect(peerActUrl('om archy', 'chrome')).toMatch(/\/peer\/om%20archy\/act\?session=chrome$/);
    expect(peerActUrl('peer-b', 'chrome')).not.toContain('100.');
  });
});
