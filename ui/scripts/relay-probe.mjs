// Opt-in real Go host/router interoperability; routine core tests need no Go.
import { spawn } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const notifications = process.argv.includes('--notifications');
const board = process.argv.includes('--board');
const server = board || notifications;
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
  await run('go', server
    ? ['test', '-c', '-o', executable, './internal/server']
    : ['build', '-o', executable, './ui/core/tests/support/relayhost'], root);
  if (notifications) {
    await writeFile(path.join(work, 'Cargo.toml'), `[package]
name = "omo-ui-jni-probe"
version = "0.1.0"
edition = "2024"
[lib]
crate-type = ["cdylib"]
path = "lib.rs"
[dependencies]
jni = "0.21"
serde_json = "1"
omo-ui-core = { path = ${JSON.stringify(path.join(root,'ui/core'))} }
`);
    await writeFile(path.join(work,'lib.rs'), `#[path = ${JSON.stringify(path.join(root,'ui/src-tauri/src/android_notifications.rs'))}]
mod android_notifications;
`);
    await run('cargo', ['build','--manifest-path',path.join(work,'Cargo.toml'),'--target-dir',path.join(root,'ui/target')], root);
    const javac = process.env.JAVA_HOME ? path.join(process.env.JAVA_HOME,'bin',process.platform === 'win32' ? 'javac.exe' : 'javac') : 'javac';
    await run(javac, ['-d',work,path.join(root,'ui/tests/jni/NativeFeed.java')], root);
  }
  const library = process.platform === 'win32' ? 'omo_ui_jni_probe.dll' : process.platform === 'darwin' ? 'libomo_ui_jni_probe.dylib' : 'libomo_ui_jni_probe.so';
  const java = process.env.JAVA_HOME ? path.join(process.env.JAVA_HOME,'bin',process.platform === 'win32' ? 'java.exe' : 'java') : 'java';
  await run('cargo', ['test', '-p', 'omo-ui-core', '--features', 'relay-integration', '--test', notifications ? 'relay_notifications_live' : board ? 'relay_board' : 'relay_native'],
    path.join(root, 'ui'), { OMO_UI_RELAY_FIXTURE: executable, ...(notifications ? { OMO_UI_JNI_JAVA: java, OMO_UI_JNI_CLASSES: work, OMO_UI_JNI_LIBRARY: path.join(root,'ui/target/debug',library) } : {}) });
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
} finally {
  if (stopping) await stopping;
  process.off('SIGINT', onInterrupt);
  process.off('SIGTERM', onInterrupt);
  await rm(work, { recursive: true, force: true });
}
