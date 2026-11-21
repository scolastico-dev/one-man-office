// Opt-in real Go host/router interoperability; routine core tests need no Go.
import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const board = process.argv.includes('--board');
const work = await mkdtemp(path.join(tmpdir(), 'omo-ui-relay-probe-'));
const executable = path.join(work, process.platform === 'win32' ? 'relayhost.exe' : 'relayhost');
const env = { ...process.env, TMPDIR: work, TMP: work, TEMP: work, GOTMPDIR: work };
let running;
let interrupted = false;
let stopping;

async function stop() {
  interrupted = true;
  const child = running;
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  if (process.platform === 'win32') {
    await new Promise((resolve, reject) => {
      const kill = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'inherit' });
      kill.once('error', reject);
      kill.once('close', code => code === 0 ? resolve() : reject(new Error(`taskkill exited ${code}`)));
    });
  } else {
    process.kill(-child.pid, 'SIGTERM');
  }
}
const onInterrupt = () => { stopping ??= stop().catch(error => { console.error(error); process.exitCode = 1; }); };
process.on('SIGINT', onInterrupt);
process.on('SIGTERM', onInterrupt);

function run(command, args, cwd, extraEnv = {}) {
  if (interrupted) throw new Error('Relay probe interrupted.');
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd, env: { ...env, ...extraEnv }, stdio: 'inherit', detached: process.platform !== 'win32',
    });
    running = child;
    child.once('error', reject);
    child.once('close', (code, signal) => {
      running = undefined;
      if (code === 0 && !interrupted) resolve();
      else reject(new Error(`${command} failed (${signal ?? code}).`));
    });
  });
}

try {
  if (board) await run('npm', ['--prefix', 'web', 'run', 'build'], root);
  await run('go', board
    ? ['test', '-c', '-o', executable, './internal/server']
    : ['build', '-o', executable, './ui/core/tests/support/relayhost'], root);
  await run('cargo', ['test', '-p', 'omo-ui-core', '--features', 'relay-integration', '--test', board ? 'relay_board' : 'relay_native'],
    path.join(root, 'ui'), { OMO_UI_RELAY_FIXTURE: executable });
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
} finally {
  if (stopping) await stopping;
  process.off('SIGINT', onInterrupt);
  process.off('SIGTERM', onInterrupt);
  await rm(work, { recursive: true, force: true });
}
