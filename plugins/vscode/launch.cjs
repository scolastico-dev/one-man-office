'use strict';
// Executed exclusively by the verified editor's bundled Node. No shell.
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn } = require('node:child_process');

function canonical(folder) {
  const real = fs.realpathSync.native(folder);
  if (!fs.statSync(real).isDirectory()) throw new Error('workspace is not a directory');
  return real;
}
function prepare(editor, root, folder, data, id, port, base) {
  if (!['code-server', 'openvscode'].includes(editor)) throw new Error('editor unavailable');
  if (!/^[a-f0-9]{32}$/.test(id) || !/^\d+$/.test(port) || +port < 1 || +port > 65535) throw new Error('invalid instance or port');
  if (!/^\/(?:proxy\/[a-f0-9]{32}\/)?$/.test(base)) throw new Error('invalid editor base path');
  folder = canonical(folder);
  if (!path.isAbsolute(root) || !path.isAbsolute(data)) throw new Error('editor paths must be absolute');
  const state = path.join(data, 'sessions', id);
  const env = {};
  for (const [key, value] of Object.entries(process.env)) {
    // Editor connection/config selectors cannot override the explicit flags.
    if (/^(OMO_|VSCODE_|CODE_SERVER_|NODE_|ELECTRON_)/i.test(key) || /^(PORT|HOST|PASSWORD|HASHED_PASSWORD|VSCODE_AGENT_FOLDER)$/i.test(key)) continue;
    env[key] = value;
  }
  const dirs = { HOME: 'home', USERPROFILE: 'home', XDG_CONFIG_HOME: 'config', XDG_DATA_HOME: 'data', XDG_CACHE_HOME: 'cache', TMPDIR: 'tmp', TEMP: 'tmp', TMP: 'tmp', APPDATA: 'config', LOCALAPPDATA: 'data' };
  for (const [key, subdir] of Object.entries(dirs)) {
    const dir = path.join(state, subdir);
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    // Windows environment keys are case-insensitive; remove inherited variants.
    for (const inherited of Object.keys(env)) if (inherited.toUpperCase() === key) delete env[inherited];
    env[key] = dir;
  }
  const user = path.join(state, 'user'), extensions = path.join(state, 'extensions');
  fs.mkdirSync(path.join(user, 'User'), { recursive: true, mode: 0o700 });
  fs.mkdirSync(extensions, { recursive: true, mode: 0o700 });
  const settingsFile = path.join(user, 'User', 'settings.json');
  // Preserve user JSONC edits after first launch. Editor server updates are
  // disabled by the product/CLI; initial UI preference reinforces that policy.
  if (!fs.existsSync(settingsFile)) fs.writeFileSync(settingsFile, JSON.stringify({ 'update.mode': 'none', 'telemetry.telemetryLevel': 'off' }), { mode: 0o600 });
  let args;
  if (editor === 'code-server') {
    const config = path.join(state, 'config.yaml');
    fs.writeFileSync(config, 'auth: none\ncert: false\n', { mode: 0o600 });
    args = [path.join(root, 'out', 'node', 'entry.js'), '--bind-addr', '127.0.0.1:' + port,
      '--auth', 'none', '--config', config, '--user-data-dir', user, '--extensions-dir', extensions,
      '--disable-telemetry', '--disable-update-check', '--ignore-last-opened',
      '--trusted-origins', '127.0.0.1:' + port, '--', folder];
  } else {
    const server = path.join(state, 'server');fs.mkdirSync(server, { recursive: true, mode: 0o700 });
    args = [path.join(root, 'out', 'server-main.js'), '--host', '127.0.0.1', '--port', port,
      '--without-connection-token', '--server-base-path', base, '--server-data-dir', server,
      '--user-data-dir', user, '--extensions-dir', extensions, '--default-folder', folder, '--telemetry-level', 'off'];
  }
  return { folder, args, env };
}
function run(args) {
  const [editor, root, folder, data, id, port, base] = args;
  const options = prepare(editor, root, folder, data, id, port, base);
  console.log('Workspace: ' + JSON.stringify(options.folder));
  console.log('Editor: ' + editor + '; loopback 127.0.0.1:' + port + '; state ' + JSON.stringify(path.join(data, 'sessions', id)));
  const child = spawn(process.execPath, options.args, { env: options.env, cwd: options.folder, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  let bound = false, ready = false, ended = false, buffer = '', collision = false, probe;
  function check(chunk, stream) {
    stream.write(chunk);
    buffer = (buffer + chunk.toString()).slice(-8192);
    if (/EADDRINUSE|address already in use/i.test(buffer)) collision = true;
    // Never trust HTTP on a candidate until this editor reports its own bind.
    if ((editor === 'code-server' && buffer.includes('HTTP server listening on http://127.0.0.1:' + port)) ||
      (editor === 'openvscode' && buffer.includes('Server bound to 127.0.0.1:' + port + ' '))) bound = true;
  }
  child.stdout.on('data', b => check(b, process.stdout));
  child.stderr.on('data', b => check(b, process.stderr));
  const timer = setInterval(() => {
    if (!bound || ready || ended || probe) return;
    const request = http.get({ host: '127.0.0.1', port: Number(port), path: editor === 'code-server' ? '/healthz' : base, timeout: 1500 }, res => {
      const ok = res.statusCode === 200;
      res.resume();
      res.on('end', () => {
        probe = null;
        if (ok && !ended && !ready) { ready = true; clearInterval(timer); clearTimeout(deadline); console.log('OMO_EDITOR_READY'); }
      });
    });
    probe = request;
    request.on('error', () => { probe = null; });
    request.on('timeout', () => request.destroy());
  }, 150);
  const deadline = setTimeout(() => {
    console.error('Editor readiness timed out; inspect runtime/ABI requirements and task output.');
    child.kill('SIGTERM');
    // omo owns the whole process tree; terminating this launcher triggers reaping.
    process.exitCode = 1;
    setTimeout(() => process.exit(1), 1000).unref();
  }, 80000);
  child.on('error', error => { console.error('Editor start failed: ' + error.code); });
  child.on('close', (code, signal) => {
    ended = true; clearInterval(timer); clearTimeout(deadline); probe?.destroy();
    if (collision) console.error('OMO_EDITOR_PORT_COLLISION');
    if (!ready) console.error('Editor exited before readiness. Check task output and runtime requirements.');
    process.exitCode = typeof code === 'number' ? code : signal ? 1 : 0;
  });
  for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => {
    ended = true; clearInterval(timer); clearTimeout(deadline); probe?.destroy(); child.kill(signal);
  });
}
try {
  if (process.argv[2] === 'identity') process.stdout.write(canonical(process.argv[3]));
  else if (process.argv[2] === 'run') run(process.argv.slice(3));
  else throw new Error('unknown editor launcher operation');
} catch (error) {
  console.error('Editor unavailable: ' + error.message);
  process.exitCode = 1;
}
