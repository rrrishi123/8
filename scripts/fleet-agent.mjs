#!/usr/bin/env node
// Hosts pull only a successful, exact four-arm CI manifest. No inbound SSH or
// deployment credential is exposed to GitHub runners.
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const arms = ['8', 'http-mcp', 'pilot', 'adapters'];
const branch = 'release/v0.0.2';
const shaRE = /^[a-f0-9]{40}$/;
const self = fileURLToPath(import.meta.url);
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));

export function validateManifest(m, heads, source) {
  if (m?.schema !== 1 || m.branch !== branch || !arms.includes(source.arm) ||
      m.source?.repository !== `rrrishi123/${source.arm}` || m.source.sha !== source.sha)
    throw new Error('CI manifest source/branch mismatch');
  for (const arm of arms) {
    if (!shaRE.test(m.revisions?.[arm] || '') || m.revisions[arm] !== heads[arm])
      throw new Error(`CI manifest does not test current ${arm} head`);
  }
  if (m.revisions[source.arm] !== source.sha) throw new Error('CI source revision mismatch');
  return m;
}

export function manifestKey(m) {
  return createHash('sha256').update(arms.map(a => `${a}:${m.revisions[a]}`).join('\n')).digest('hex').slice(0, 20);
}

export function buildSHA(body) {
  return (typeof body?.build === 'string' ? body.build : body?.build?.sha || body?.build?.revision) || body?.build_sha || body?.revision || '';
}

async function run(exe, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(exe, args, { cwd: options.cwd, env: { ...process.env, ...options.env }, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    const timeout = setTimeout(() => child.kill('SIGTERM'), options.timeout || 30 * 60 * 1000);
    child.stdout.on('data', b => { stdout = (stdout + b).slice(-4 * 1024 * 1024); if (options.stream) process.stdout.write(b); });
    child.stderr.on('data', b => { stderr = (stderr + b).slice(-64 * 1024); if (options.stream) process.stderr.write(b); });
    child.on('error', e => { clearTimeout(timeout); reject(e); });
    child.on('close', code => { clearTimeout(timeout); code === 0 ? resolve(stdout.trim()) : reject(new Error(`${exe} failed (${code}): ${stderr.slice(-2000)}`)); });
  });
}

async function atomicJSON(file, value) {
  await fs.mkdir(path.dirname(file), { recursive: true });
  const tmp = `${file}.${process.pid}.tmp`;
  await fs.writeFile(tmp, JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
  await fs.rename(tmp, file);
}

async function readJSON(file) { try { return JSON.parse(await fs.readFile(file, 'utf8')); } catch { return null; } }
async function api(route) { return JSON.parse(await run('gh', ['api', route], { timeout: 60000 })); }

async function headsNow() {
  const heads = {};
  for (const arm of arms) {
    const ref = await api(`repos/rrrishi123/${arm}/git/ref/heads/${branch}`);
    if (!shaRE.test(ref.object?.sha || '')) throw new Error(`No release head for ${arm}`);
    heads[arm] = ref.object.sha;
  }
  return heads;
}

async function candidate(cfg, heads) {
  const runs = [];
  for (const arm of arms) {
    // A missing workflow is a configuration error, never permission to deploy.
    const listing = await api(`repos/rrrishi123/${arm}/actions/workflows/fleet-release.yml/runs?branch=${encodeURIComponent(branch)}&event=push&status=success&head_sha=${heads[arm]}&per_page=5`);
    for (const run of listing.workflow_runs || []) {
      if (run.head_sha === heads[arm] && run.conclusion === 'success' && run.event === 'push')
        runs.push({ arm, sha: heads[arm], id: run.id, at: run.updated_at });
    }
  }
  runs.sort((a, b) => b.at.localeCompare(a.at));
  for (const source of runs) {
    const dir = await fs.mkdtemp(path.join(cfg.state, 'receipt-'));
    try {
      await run('gh', ['run', 'download', String(source.id), '--repo', `rrrishi123/${source.arm}`, '--name', 'fleet-release', '--dir', dir]);
      const manifest = validateManifest(await readJSON(path.join(dir, 'fleet-release.json')), heads, source);
      return { manifest, source };
    } catch (e) { console.log(`CI run ${source.id} ineligible: ${e.message}`); }
    finally { await fs.rm(dir, { recursive: true, force: true }); }
  }
  return null;
}

async function checkout(cfg, manifest) {
  const root = path.join(cfg.state, 'source');
  await fs.mkdir(root, { recursive: true });
  for (const arm of arms) {
    const dir = path.join(root, arm);
    if (!await fs.stat(path.join(dir, '.git')).catch(() => false))
      await run('git', ['clone', '--no-checkout', `https://github.com/rrrishi123/${arm}.git`, dir]);
    // These are private deployment checkouts, never the operator's working tree.
    if (await run('git', ['status', '--porcelain', '--untracked-files=no'], { cwd: dir }))
      throw new Error(`Deployment checkout ${arm} has tracked edits; refusing to discard`);
    await run('git', ['fetch', '--no-tags', 'origin', manifest.revisions[arm]], { cwd: dir });
    await run('git', ['checkout', '--detach', manifest.revisions[arm]], { cwd: dir });
  }
  return root;
}

async function witness(cfg, text) {
  const response = await fetch(`${cfg.witness}/work`, { method: 'POST', signal: AbortSignal.timeout(10000),
    headers: { 'Content-Type': 'application/json', 'X-8-Actor': `fleet-deploy/${cfg.host}`, ...(process.env.EIGHT_TOKEN ? { 'X-8-Token': process.env.EIGHT_TOKEN } : {}) },
    body: JSON.stringify({ by: `fleet-deploy/${cfg.host}`, kind: 'record', text }) });
  if (!response.ok) throw new Error(`witness HTTP ${response.status}`);
  return { receipt: response.headers.get('x-8-witness'), ...(await response.json()) };
}

export async function attest(cfg, expected, attempts = 45) {
  let reason = 'no response';
  for (let i = 0; i < attempts; i++) {
    try {
      const response = await fetch(cfg.attest, { signal: AbortSignal.timeout(3000) });
      const body = await response.json();
      if (response.ok && body.alive !== false && buildSHA(body) === expected)
        return { url: cfg.attest, sha: buildSHA(body), receipt: response.headers.get('x-8-witness'), body };
      reason = `expected ${expected}, observed ${buildSHA(body) || 'missing build SHA'}`;
    } catch (e) { reason = e.message; }
    if (i + 1 < attempts) await pause(1000);
  }
  throw new Error(`running-binary attestation failed: ${reason}`);
}

async function colimaDeploy(cfg, root, manifest) {
  const image = `eight-fleet:ci-${manifestKey(manifest)}`;
  await run('npm', ['ci'], { cwd: path.join(root, '8/web'), stream: true });
  await run('bash', [path.join(root, '8/deploy/build-runtime.sh')], { stream: true,
    env: { EIGHT_REPO: root, FLEET_IMAGE: image } });
  // Do not deploy a manifest superseded while compilation was in flight.
  validateManifest(manifest, await headsNow(), { arm: manifest.source.repository.split('/')[1], sha: manifest.source.sha });
  const old = JSON.parse(await run('docker', ['inspect', cfg.container]))[0];
  const volume = old.Mounts.find(m => m.Type === 'volume' && m.Destination === '/root/.8')?.Name;
  if (!volume) throw new Error('Runtime has no durable named state volume; refusing replacement');
  const oldEnv = Object.fromEntries(old.Config.Env.map(e => { const n = e.indexOf('='); return [e.slice(0, n), e.slice(n + 1)]; }));
  const backup = `${cfg.container}-rollback-${Date.now()}`;
  const ports = old.HostConfig.PortBindings['7070/tcp'];
  if (ports?.length !== 1 || ports[0].HostIp !== '127.0.0.1') throw new Error('Expected one loopback runtime port');
  await run('docker', ['stop', '-t', '15', cfg.container]);
  await run('docker', ['rename', cfg.container, backup]);
  try {
    await run('bash', [path.join(root, '8/deploy/run-colima.sh')], { stream: true, env: {
      FLEET_IMAGE: image, FLEET_CONTAINER: cfg.container, FLEET_VOLUME: volume,
      FLEET_PORT: ports[0].HostPort, FLEET_MEMORY: String(old.HostConfig.Memory),
      FLEET_CPUS: String(old.HostConfig.NanoCpus / 1e9 || 1),
      PEER_HOST: oldEnv.PEER_HOST || old.Config.Hostname,
      PEER_HUB: oldEnv.PEER_HUB || '', PEER_TOKEN: oldEnv.PEER_TOKEN || '',
      // Resolve the named browser containers again; their IPs can change on T6 migration.
      BROWSER_ENDPOINTS: process.env.BROWSER_ENDPOINTS || '' } });
    const proof = await attest(cfg, manifest.revisions['8']);
    // Keep exactly the last stopped runtime for rollback, including its original config.
    const previous = await readJSON(path.join(cfg.state, 'rollback.json'));
    if (previous?.container && previous.container !== backup) await run('docker', ['rm', previous.container]).catch(() => {});
    await atomicJSON(path.join(cfg.state, 'rollback.json'), { container: backup, image: old.Image });
    return proof;
  } catch (e) {
    await run('docker', ['rm', '-f', cfg.container]).catch(() => {});
    await run('docker', ['rename', backup, cfg.container]);
    await run('docker', ['start', cfg.container]);
    throw new Error(`Deploy rolled back: ${e.message}`);
  }
}

async function nativeDeploy(cfg, root, manifest) {
  // Test/build in private checkouts first. The live checkout is touched only
  // after all arms pass, and only if clean and a fast-forward on the release branch.
  await run('bash', [path.join(root, '8/scripts/fleet-ci.sh')], { stream: true, cwd: root,
    env: { ROOT: root, SOURCE_REPOSITORY: manifest.source.repository, SOURCE_SHA: manifest.source.sha } });
  validateManifest(manifest, await headsNow(), { arm: manifest.source.repository.split('/')[1], sha: manifest.source.sha });
  for (const arm of arms) {
    const cwd = path.join(cfg.root, arm);
    if (await run('git', ['branch', '--show-current'], { cwd }) !== branch || await run('git', ['status', '--porcelain'], { cwd }))
      throw new Error(`${arm}: live checkout must be clean on ${branch}`);
    await run('git', ['fetch', '--no-tags', 'origin', manifest.revisions[arm]], { cwd });
    await run('git', ['merge-base', '--is-ancestor', 'HEAD', manifest.revisions[arm]], { cwd });
  }
  for (const arm of arms) {
    const cwd = path.join(cfg.root, arm);
    await run('git', ['merge', '--ff-only', manifest.revisions[arm]], { cwd });
    await run('bash', ['build.sh'], { cwd, stream: true });
  }
  await run('npm', ['ci'], { cwd: path.join(cfg.root, '8/web'), stream: true });
  await run('npm', ['run', 'build'], { cwd: path.join(cfg.root, '8/web'), stream: true });
  await run('bash', [process.env.FLEET_RESTART_SCRIPT || path.join(cfg.root, '8/scripts/collector-restart.sh')], { stream: true });
  return attest(cfg, manifest.revisions['8']);
}

export async function tick(cfg) {
  const heads = await headsNow();
  const previous = await readJSON(path.join(cfg.state, 'deployed.json'));
  const key = manifestKey({ revisions: heads });
  if (previous?.key === key) {
    // The receipt is historical; check the live binary on every reconciliation.
    try { await attest(cfg, heads['8'], 1); return { state: 'current', sha: heads['8'] }; }
    catch { console.log('Live build drifted from deployment receipt; reconciling'); }
  }
  const selected = await candidate(cfg, heads);
  if (!selected) return { state: 'waiting-for-ci', heads };
  const { manifest, source } = selected;
  await atomicJSON(path.join(cfg.state, 'pending.json'), { manifest, source });
  const root = await checkout(cfg, manifest);
  await witness(cfg, `DEPLOY START ${cfg.host}: successful CI ${source.arm} run ${source.id}; pinned four-arm manifest ${key}, collector ${heads['8']}.`);
  const proof = cfg.mode === 'colima' ? await colimaDeploy(cfg, root, manifest) : await nativeDeploy(cfg, root, manifest);
  const receipt = await witness(cfg, `DEPLOY VERIFIED ${cfg.host}: CI run ${source.id}, manifest ${key}, running collector ${proof.sha}, attestation ${proof.receipt || proof.url}. Revisions ${JSON.stringify(manifest.revisions)}.`);
  await atomicJSON(path.join(cfg.state, 'deployed.json'), { key, manifest, source, proof, receipt, at: new Date().toISOString() });
  return { state: 'deployed', sha: proof.sha, receipt };
}

export function config(env = process.env) {
  const host = env.FLEET_HOST || os.hostname();
  const mode = env.FLEET_MODE;
  if (!['native', 'colima'].includes(mode)) throw new Error('Set FLEET_MODE=native or colima explicitly');
  const interval = Number(env.FLEET_INTERVAL || 60);
  if (!Number.isFinite(interval) || interval < 15) throw new Error('FLEET_INTERVAL must be at least 15 seconds');
  return { host, mode, root: env.FLEET_ROOT || path.resolve(path.dirname(self), '../..'),
    state: env.FLEET_STATE_DIR || path.join(os.homedir(), '.8/fleet', mode),
    container: env.FLEET_CONTAINER || 'eight-fleet', witness: env.FLEET_WITNESS || 'http://127.0.0.1:7070',
    attest: env.FLEET_ATTEST_URL || `http://127.0.0.1:${mode === 'colima' ? 7071 : 7070}/health`,
    interval: interval * 1000 };
}

async function main() {
  const cfg = config();
  await fs.mkdir(cfg.state, { recursive: true });
  const lock = path.join(cfg.state, 'agent.lock');
  try { await fs.mkdir(lock); } catch {
    const owner = Number(await fs.readFile(path.join(lock, 'pid'), 'utf8').catch(() => '0'));
    if (!owner) throw new Error('Agent lock has no owner; inspect before removing');
    try { process.kill(owner, 0); throw new Error(`Fleet agent already active (${owner})`); }
    catch (e) { if (e.code !== 'ESRCH') throw e; }
    await fs.rm(lock, { recursive: true }); await fs.mkdir(lock);
  }
  await fs.writeFile(path.join(lock, 'pid'), String(process.pid));
  let stopped = false;
  process.on('SIGTERM', () => { stopped = true; });
  process.on('SIGINT', () => { stopped = true; });
  try {
    do {
      try { console.log(new Date().toISOString(), JSON.stringify(await tick(cfg))); }
      catch (e) {
        console.error(new Date().toISOString(), e.message);
        await atomicJSON(path.join(cfg.state, 'error.json'), { at: new Date().toISOString(), error: e.message });
        if (process.argv.includes('--once')) throw e;
      }
      if (process.argv.includes('--once')) break;
      for (let t = 0; t < cfg.interval && !stopped; t += 1000) await pause(1000);
    } while (!stopped);
  } finally { await fs.rm(lock, { recursive: true, force: true }); }
}

if (process.argv[1] && path.resolve(process.argv[1]) === self)
  main().catch(e => { console.error(e.message); process.exitCode = 1; });
