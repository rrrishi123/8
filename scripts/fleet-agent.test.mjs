import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { execFile } from 'node:child_process';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import { arms, validateManifest, manifestKey, buildSHA, config, attest, checkoutRepository } from './fleet-agent.mjs';

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

const exec = promisify(execFile);
async function git(cwd, ...args) {
  return (await exec('git', args, { cwd })).stdout.trim();
}
async function repositoryFixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'fleet-checkout-test-'));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const origin = path.join(root, 'origin');
  await fs.mkdir(origin);
  await git(origin, 'init', '-b', 'main');
  await git(origin, 'config', 'user.name', 'Fleet test');
  await git(origin, 'config', 'user.email', 'fleet@example.invalid');
  await fs.writeFile(path.join(origin, 'file'), 'default branch\n');
  await git(origin, 'add', 'file');
  await git(origin, '-c', 'commit.gpgsign=false', 'commit', '-m', 'default');
  const first = await git(origin, 'rev-parse', 'HEAD');
  await git(origin, 'checkout', '-b', 'release/v0.0.2');
  await fs.writeFile(path.join(origin, 'file'), 'tested release\n');
  await git(origin, '-c', 'commit.gpgsign=false', 'commit', '-am', 'release');
  const second = await git(origin, 'rev-parse', 'HEAD');
  await git(origin, 'checkout', 'main');
  return { root, origin, dir: path.join(root, 'deploy'), first, second };
}

test('fresh clone initializes the pinned release before the dirty-check gate', async t => {
  const { dir, origin, first, second } = await repositoryFixture(t);
  await checkoutRepository(dir, origin, second);
  assert.equal(await git(dir, 'rev-parse', 'HEAD'), second);
  assert.equal(await fs.readFile(path.join(dir, 'file'), 'utf8'), 'tested release\n');
  assert.equal(await git(dir, 'status', '--porcelain'), '');
  // Repeat ticks and clean updates also work; neither follows origin/main.
  await checkoutRepository(dir, origin, second);
  await checkoutRepository(dir, origin, first);
  assert.equal(await git(dir, 'rev-parse', 'HEAD'), first);
});

test('existing staged and unstaged operator edits are preserved', async t => {
  const { dir, origin, first, second } = await repositoryFixture(t);
  await checkoutRepository(dir, origin, first);
  await fs.writeFile(path.join(dir, 'file'), 'operator edit\n');
  await assert.rejects(checkoutRepository(dir, origin, second), /tracked edits/);
  await git(dir, 'add', 'file');
  await assert.rejects(checkoutRepository(dir, origin, second), /tracked edits/);
  assert.equal(await fs.readFile(path.join(dir, 'file'), 'utf8'), 'operator edit\n');
  assert.equal(await git(dir, 'rev-parse', 'HEAD'), first);
});

test('failed bootstrap leaves no partial checkout and the next tick recovers', async t => {
  const { root, dir, origin, second } = await repositoryFixture(t);
  await assert.rejects(checkoutRepository(dir, origin, 'f'.repeat(40)), /git failed/);
  assert.deepEqual(await fs.readdir(root), ['origin']);
  await checkoutRepository(dir, origin, second);
  assert.equal(await git(dir, 'rev-parse', 'HEAD'), second);
});
