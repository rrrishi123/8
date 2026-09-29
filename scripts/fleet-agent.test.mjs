import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { arms, validateManifest, manifestKey, buildSHA, config, attest } from './fleet-agent.mjs';

const heads = Object.fromEntries(arms.map((a, i) => [a, String(i + 1).repeat(40)]));
const source = { arm: '8', sha: heads['8'] };
const manifest = () => ({ schema: 1, branch: 'release/v0.0.2', source: { repository: 'rrrishi123/8', sha: heads['8'] }, revisions: { ...heads } });

test('accepts exactly the four tested release revisions', () => {
  assert.equal(validateManifest(manifest(), heads, source).revisions.pilot, heads.pilot);
});
test('rejects a main-branch or foreign-source receipt', () => {
  for (const modify of [m => m.branch = 'main', m => m.source.repository = 'outsider/8', m => m.source.sha = heads.pilot]) {
    const m = manifest(); modify(m); assert.throws(() => validateManifest(m, heads, source));
  }
});
test('a successful but superseded or incomplete gate cannot deploy', () => {
  for (const arm of arms) {
    const m = manifest(); m.revisions[arm] = 'f'.repeat(40);
    assert.throws(() => validateManifest(m, heads, source));
    delete m.revisions[arm]; assert.throws(() => validateManifest(m, heads, source));
  }
});
test('deployment identity includes every arm independently of key order', () => {
  assert.equal(manifestKey(manifest()), manifestKey({ revisions: Object.fromEntries(Object.entries(heads).reverse()) }));
  for (const arm of arms) {
    const m = manifest(); m.revisions[arm] = 'f'.repeat(40);
    assert.notEqual(manifestKey(manifest()), manifestKey(m));
  }
});
test('reads the actual collector build contract', () => {
  assert.equal(buildSHA({ build: heads['8'] }), heads['8']);
  assert.equal(buildSHA({ build: { sha: heads['8'] } }), heads['8']);
  assert.equal(buildSHA({ alive: true }), '');
});
test('deployment needs an explicit host mode and bounded polling', () => {
  assert.throws(() => config({}));
  assert.throws(() => config({ FLEET_MODE: 'native', FLEET_INTERVAL: 'NaN' }));
  assert.throws(() => config({ FLEET_MODE: 'native', FLEET_INTERVAL: '0' }));
  assert.equal(config({ FLEET_MODE: 'colima' }).attest, 'http://127.0.0.1:7071/health');
});
test('attestation rejects healthy stale binaries and retains witness receipt', async t => {
  let body = { alive: true, build: heads.pilot };
  const server = http.createServer((req, res) => {
    res.setHeader('X-8-Witness', 'test receipt'); res.end(JSON.stringify(body));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const cfg = { attest: `http://127.0.0.1:${server.address().port}/health` };
  await assert.rejects(attest(cfg, heads['8'], 1), /expected/);
  body = { alive: false, build: heads['8'] };
  await assert.rejects(attest(cfg, heads['8'], 1));
  body = { alive: true, build: heads['8'] };
  assert.equal((await attest(cfg, heads['8'], 1)).receipt, 'test receipt');
});
