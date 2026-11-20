'use strict';
// Executed exclusively by the verified editor's bundled Node. No shell.
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { createHash } = require('node:crypto');
const { spawn } = require('node:child_process');
const {detectLocal, prepareProfile, incompatibleExtensions} = require('./profile.cjs');

function canonical(folder) {
  const real = fs.realpathSync.native(folder);
  if (!fs.statSync(real).isDirectory()) throw new Error('workspace is not a directory');
  return real;
}
function prepare(editor, root, folder, data, id, port, base, runtime) {
  if (!['code-server', 'openvscode'].includes(editor)) throw new Error('editor unavailable');
  if (!/^[a-f0-9]{32}$/.test(id) || !/^\d+$/.test(port) || +port < 1 || +port > 65535) throw new Error('invalid instance or port');
  if (!/^\/(?:proxy\/[a-f0-9]{32}\/)?$/.test(base)) throw new Error('invalid editor base path');
  folder = canonical(folder);
  if (!path.isAbsolute(root) || !path.isAbsolute(data)) throw new Error('editor paths must be absolute');
  if (typeof runtime !== 'string' || !path.isAbsolute(runtime) || !fs.statSync(runtime).isDirectory()) throw new Error('editor runtime must be an absolute temporary directory');
  // OpenVSCode's longest IPC name includes a full UUID. Check UTF-8 bytes of
  // the literal path passed to Unix, not JS characters or an expanded realpath.
  const longestSocket = path.join(runtime, 'vscode-ipc-11111111-1111-4111-8111-111111111111.sock');
  if (process.platform !== 'win32' && Buffer.byteLength(longestSocket, 'utf8') > (process.platform === 'darwin' ? 103 : 107)) {
    throw new Error('editor runtime path is too long for Unix IPC; use a shorter system temporary directory');
  }
  const state = path.join(data, 'sessions', id);
  // User preferences/extensions belong to the editor, not a folder or release.
  // Runtime/socket state stays private to each simultaneously running folder.
  const profile = path.join(data, 'profiles', editor);
  const local = detectLocal();
  const env = {};
  for (const [key, value] of Object.entries(process.env)) {
    // Editor connection/config selectors cannot override the explicit flags.
    if (/^(OMO_|VSCODE_|CODE_SERVER_|CS_|NODE_|ELECTRON_)/i.test(key) || /^(PORT|HOST|PASSWORD|HASHED_PASSWORD|EXTENSIONS_GALLERY|EDITOR_HOST_XDG_RUNTIME_DIR)$/i.test(key)) continue;
    env[key] = value;
  }
  // Persistent host tool roots stay intact. omo owns this short per-task lease
  // and its cleanup. XDG_RUNTIME_DIR must select it for editor IPC; fresh user
  // profiles restore the host value in integrated terminals via a VS setting.
  env.EDITOR_HOST_XDG_RUNTIME_DIR = process.env.XDG_RUNTIME_DIR || '';
  const dirs = { TMPDIR: runtime, TEMP: runtime, TMP: runtime, XDG_RUNTIME_DIR: runtime,
    VSCODE_PORTABLE: path.join(profile, 'portable'), VSCODE_AGENT_FOLDER: path.join(state, 'server'),
    VSCODE_CODE_CACHE_PATH: path.join(state, 'cache', 'code') };
  for (const [key, dir] of Object.entries(dirs)) {
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    // Windows environment keys are case-insensitive; remove inherited variants.
    for (const inherited of Object.keys(env)) if (inherited.toUpperCase() === key) delete env[inherited];
    env[key] = dir;
  }
  // Align portable-mode defaults (argv.json, policy/shared data) with the
  // explicit user-data flag, including upstream Windows APPDATA precedence.
  const user = path.join(env.VSCODE_PORTABLE, 'user-data');
  const server = env.VSCODE_AGENT_FOLDER, logs = path.join(state, 'logs'), downloads = path.join(profile, 'cache', 'extension-downloads');
  fs.mkdirSync(path.join(user, 'User'), { recursive: true, mode: 0o700 });
  fs.mkdirSync(path.join(user, 'Machine'), { recursive: true, mode: 0o700 });
  fs.mkdirSync(logs, { recursive: true, mode: 0o700 });
  fs.mkdirSync(downloads, { recursive: true, mode: 0o700 });
  const settingsFile = path.join(user, 'User', 'settings.json');
  // The remote browser client reads Machine/settings.json for host settings;
  // User/settings.json alone does not configure its integrated terminal.
  // Initialize both scopes once, preserving existing JSONC verbatim. Exclusive
  // creation also prevents concurrent first launches from replacing user edits.
  const initialSettings = JSON.stringify({
    'update.mode': 'none', 'telemetry.telemetryLevel': 'off',
    ...(local ? {} : {'workbench.colorTheme': 'Default Dark Modern', 'chat.disableAIFeatures': true}),
    'terminal.integrated.env.linux': { XDG_RUNTIME_DIR: '${env:EDITOR_HOST_XDG_RUNTIME_DIR}' },
    'terminal.integrated.env.osx': { XDG_RUNTIME_DIR: '${env:EDITOR_HOST_XDG_RUNTIME_DIR}' },
  });
  try { fs.writeFileSync(settingsFile, initialSettings, { mode: 0o600, flag: 'wx' }); }
  catch (error) { if (error.code !== 'EEXIST') throw error; }
  try { fs.copyFileSync(settingsFile, path.join(user, 'Machine', 'settings.json'), fs.constants.COPYFILE_EXCL); }
  catch (error) { if (error.code !== 'EEXIST') throw error; }
  const {extensions, builtins, preload: fileRootPreload} = prepareProfile({editor, root, profile, state, data, user, local});
  let args;
  if (editor === 'code-server') {
    const config = path.join(state, 'config.yaml');
    fs.writeFileSync(config, 'auth: none\ncert: false\n', { mode: 0o600 });
    // In the pinned publisher build wrapper logs/heartbeat/TLS pipes use
    // util.paths independently of --user-data-dir. Override that path provider
    // before loading entry.js, without changing tool-visible host roots. Node
    // fork inherits this preload for code-server's wrapper child as well.
    const publisherPaths = { data: path.join(state, 'data'), config: path.join(state, 'config'), runtime };
    for (const dir of Object.values(publisherPaths)) fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    const preload = path.join(state, 'paths.cjs');
    fs.writeFileSync(preload, 'Object.assign(require(' + JSON.stringify(path.join(root, 'out', 'node', 'util.js')) + ').paths,' + JSON.stringify(publisherPaths) + ');\n', { mode: 0o600 });
    const socket = process.platform === 'win32' ? '\\\\.\\pipe\\omo-editor-' + createHash('sha256').update(runtime).digest('hex').slice(0, 24) : path.join(runtime, 'ipc');
    args = ['--require', preload, path.join(root, 'out', 'node', 'entry.js'), '--bind-addr', '127.0.0.1:' + port,
      '--auth', 'none', '--enable-remote-storage', '--config', config, '--user-data-dir', user, '--extensions-dir', extensions, '--builtin-extensions-dir', builtins,
      '--session-socket', socket, '--vscode-option', 'server-data-dir=' + server, '--vscode-option', 'logsPath=' + logs,
      '--vscode-option', 'extensions-download-dir=' + downloads,
      '--disable-telemetry', '--disable-update-check', '--disable-workspace-trust', '--ignore-last-opened',
      '--trusted-origins', '127.0.0.1:' + port, '--', folder];
  } else {
    args = ['--require', fileRootPreload, path.join(root, 'out', 'server-main.js'), '--host', '127.0.0.1', '--port', port,
      '--without-connection-token', '--disable-workspace-trust',
      // The pinned publisher turns an explicit '/' into an empty client base,
      // breaking WebSocket URLs. Omission keeps its '/' default for subdomains.
      ...(base === '/' ? [] : ['--server-base-path', base]), '--server-data-dir', server,
      '--user-data-dir', user, '--extensions-dir', extensions, '--builtin-extensions-dir', builtins, '--extensions-download-dir', downloads, '--logsPath', logs,
      '--default-folder', folder, '--telemetry-level', 'off'];
  }
  const skipped = local ? incompatibleExtensions(extensions) : [];
  const skippedPath = path.join(profile, 'incompatible-extensions.json');
  fs.writeFileSync(skippedPath, JSON.stringify(skipped, null, 2) + '\n', {mode: 0o600});
  return { folder, args, env, local, skipped, skippedPath };
}
function run(args) {
  const [editor, root, folder, data, id, port, base, runtime] = args;
  const options = prepare(editor, root, folder, data, id, port, base, runtime);
  console.log('Workspace: ' + JSON.stringify(options.folder));
  console.log('Editor: ' + editor + '; loopback 127.0.0.1:' + port + '; state ' + JSON.stringify(path.join(data, 'sessions', id)));
  console.log('Runtime: ' + JSON.stringify(runtime));
  console.log(options.local ? 'Shared desktop profile: ' + JSON.stringify(options.local) : 'Persistent private browser profile (no desktop installation found).');
  if (options.skipped.length) console.log('Incompatible desktop extension list: ' + JSON.stringify(options.skippedPath));
  for (const id of options.skipped) console.log('Skipped incompatible desktop extension (UI only, no web entry point): ' + JSON.stringify(id));
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
  if (process.argv[2] === 'identity') process.stdout.write('\nOMO_EDITOR_IDENTITY:' + Buffer.from(canonical(process.argv[3]), 'utf8').toString('hex') + '\n');
  else if (process.argv[2] === 'run') run(process.argv.slice(3));
  else throw new Error('unknown editor launcher operation');
} catch (error) {
  console.error('Editor unavailable: ' + error.message);
  process.exitCode = 1;
}
