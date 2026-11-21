'use strict';
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {createHash, randomUUID} = require('node:crypto');
const {patchRenderer} = require('./secret-provider.cjs');

// Default profiles, in a stable order when more than one product is installed.
// Discovery only reads directory metadata, never native databases or secrets.
function detectLocal({platform = process.platform, home = os.homedir(), env = process.env} = {}) {
  const config = platform === 'darwin' ? path.join(home, 'Library', 'Application Support') :
    platform === 'win32' ? env.APPDATA || path.join(home, 'AppData', 'Roaming') : env.XDG_CONFIG_HOME || path.join(home, '.config');
  for (const [name, product, extensions] of [
    ['Microsoft VS Code', 'Code', '.vscode'], ['VS Code Insiders', 'Code - Insiders', '.vscode-insiders'],
    ['VSCodium', 'VSCodium', '.vscode-oss'], ['VSCodium Insiders', 'VSCodium - Insiders', '.vscode-oss-insiders'],
    ['Code OSS', 'Code - OSS', '.vscode-oss'],
  ]) {
    const user = path.join(config, product, 'User');
    try {
      if (fs.statSync(user).isDirectory()) return {name, user, extensions: path.join(home, extensions, 'extensions')};
    } catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  return null;
}

function prepareProfile({editor, root, profile, state, data, user, local, secretCapability}) {
  const extensions = local?.extensions || path.join(profile, 'extensions');
  const sharedUser = local?.user || path.join(profile, 'preferences', 'User');
  for (const dir of [extensions, sharedUser]) fs.mkdirSync(dir, {recursive: true, mode: 0o700});
  if (!local) {
    // Seed once from the retained profile. The bridge owns the persistent copy
    // thereafter, including when OpenVSCode gets a new browser origin.
    const source = path.join(user, 'User', 'settings.json');
    try { fs.copyFileSync(source, path.join(sharedUser, 'settings.json'), fs.constants.COPYFILE_EXCL); }
    catch (error) { if (error.code !== 'EEXIST' && error.code !== 'ENOENT') throw error; }
  }
  const builtins = path.join(state, 'builtins');
  fs.mkdirSync(builtins, {recursive: true, mode: 0o700});
  const publisher = editor === 'code-server' ? path.join(root, 'lib', 'vscode', 'extensions') : path.join(root, 'extensions');
  let entries;
  try { entries = fs.readdirSync(publisher); }
  catch (error) { if (error.code !== 'ENOENT') throw error; entries = []; }
  for (const name of entries) {
    const target = path.join(publisher, name), link = path.join(builtins, name);
    if (!fs.statSync(target).isDirectory()) continue;
    try {
      if (fs.readlinkSync(link) === target) continue;
      fs.unlinkSync(link);
    } catch (error) { if (error.code !== 'ENOENT') throw error; }
    fs.symlinkSync(target, link, process.platform === 'win32' ? 'junction' : 'dir');
  }
  const bridge = path.join(builtins, 'omo.profile-bridge');
  fs.mkdirSync(bridge, {recursive: true, mode: 0o700});
  for (const name of ['profile-bridge.cjs', 'preference-sync.cjs', 'preference-history.cjs']) fs.copyFileSync(path.join(__dirname, name), path.join(bridge, name));
  fs.cpSync(path.join(__dirname, 'vendor'), path.join(bridge, 'vendor'), {recursive: true});
  fs.writeFileSync(path.join(bridge, 'package.json'), JSON.stringify({
    name: 'profile-bridge', publisher: 'omo', version: '1.0.0', displayName: 'omo shared preferences',
    engines: {vscode: '^1.109.0'}, activationEvents: ['*'], main: './profile-bridge.cjs',
    extensionKind: ['workspace'], capabilities: {untrustedWorkspaces: {supported: true}},
  }), {mode: 0o600});
  const locks = path.join(data, 'preference-locks');
  fs.mkdirSync(locks, {recursive: true, mode: 0o700});
  fs.writeFileSync(path.join(bridge, 'profile.json'), JSON.stringify({
    sharedUser, browserUser: editor === 'code-server' ? {scheme: 'vscode-remote', path: path.join(user, 'User'), native: true} : {scheme: 'vscode-userdata', path: '/User'},
    lock: path.join(locks, createHash('sha256').update(sharedUser).digest('hex') + '.sqlite'),
    backups: path.join(profile, 'preference-conflicts'),
    nativeHistory: editor === 'code-server' ? path.join(profile, 'preference-history') : undefined,
    adoptBrowserOnce: local ? undefined : path.join(profile, 'preferences', '.browser-imported'),
  }), {mode: 0o600});
  let preload;
  if (editor === 'openvscode' || secretCapability) {
    // This pinned bootstrap assigns builtin-extensions-dir from FileAccess,
    // overriding the CLI flag. Give FileAccess a private root whose publisher
    // files remain links to the verified generation and whose extensions add
    // the bridge. The immutable artifact itself is never changed.
    const overlay = path.join(state, 'publisher');
    const publisherRoot = editor === 'code-server' ? path.join(root, 'lib', 'vscode') : root;
    fs.mkdirSync(overlay, {recursive: true, mode: 0o700});
    function link(target, destination) {
      if (process.platform === 'win32' && fs.statSync(target).isFile()) {
        try {
          const original = fs.statSync(target), existing = fs.statSync(destination);
          if (original.dev === existing.dev && original.ino === existing.ino) return;
          fs.unlinkSync(destination);
        } catch (error) { if (error.code !== 'ENOENT') throw error; }
        fs.linkSync(target, destination); return;
      }
      try { if (fs.readlinkSync(destination) === target) return; fs.unlinkSync(destination); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      fs.symlinkSync(target, destination, fs.statSync(target).isDirectory() ? (process.platform === 'win32' ? 'junction' : 'dir') : 'file');
    }
    function rendererTree(source, destination, parts) {
      try { if (fs.lstatSync(destination).isSymbolicLink()) fs.unlinkSync(destination); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      fs.mkdirSync(destination, {recursive: true, mode: 0o700});
      for (const name of fs.readdirSync(source)) {
        const from = path.join(source, name), to = path.join(destination, name);
        if (name !== parts[0]) link(from, to);
        else if (parts.length > 1) rendererTree(from, to, parts.slice(1));
        else {
          let workbench = fs.readFileSync(from, 'utf8');
          if (editor === 'openvscode') {
            if (!workbench.includes('vscode-web-db') || !workbench.includes('vscode-userdata-store')) throw new Error('unsupported publisher preference storage');
            workbench = fs.readFileSync(path.join(__dirname, 'browser-history.cjs'), 'utf8') + '\n' + workbench;
          }
          if (secretCapability) workbench = patchRenderer(workbench, secretCapability);
          const temporary = to + '.omo-' + randomUUID();
          try {
            fs.writeFileSync(temporary, workbench, {mode: 0o600, flag: 'wx'});
            fs.renameSync(temporary, to);
          } finally { try { fs.unlinkSync(temporary); } catch (error) { if (error.code !== 'ENOENT') throw error; } }
        }
      }
    }
    const renderer = ['vs', 'code', 'browser', 'workbench', 'workbench.js'];
    for (const name of new Set([...(fs.existsSync(publisherRoot) ? fs.readdirSync(publisherRoot) : []), 'extensions'])) {
      const target = name === 'extensions' ? builtins : path.join(publisherRoot, name), destination = path.join(overlay, name);
      if (name === 'out' && fs.existsSync(path.join(publisherRoot, 'out', ...renderer))) {
        rendererTree(target, destination, renderer); continue;
      }
      link(target, destination);
    }
    preload = path.join(state, 'file-root.cjs');
    fs.writeFileSync(preload, 'let root;Object.defineProperty(globalThis,"_VSCODE_FILE_ROOT",{configurable:true,get(){return root},set(value){root=value===' +
      JSON.stringify(path.join(publisherRoot, 'out')) + '?' + JSON.stringify(path.join(overlay, 'out')) + ':value}});\n', {mode: 0o600});
  }
  return {extensions, sharedUser, builtins, preload};
}
function incompatibleExtensions(extensions) {
  const result = [];
  for (const entry of fs.readdirSync(extensions, {withFileTypes: true})) {
    if (!entry.isDirectory() && !entry.isSymbolicLink()) continue;
    const filename = path.join(extensions, entry.name, 'package.json');
    try {
      const stat = fs.statSync(filename);
      if (!stat.isFile() || stat.size > 1024 * 1024) continue;
      const manifest = JSON.parse(fs.readFileSync(filename, 'utf8'));
      if (manifest.main && !manifest.browser && Array.isArray(manifest.extensionKind) && manifest.extensionKind.length &&
        manifest.extensionKind.every(kind => kind === 'ui') && typeof manifest.publisher === 'string' && typeof manifest.name === 'string') {
        result.push(manifest.publisher + '.' + manifest.name);
      }
    } catch (error) { if (error.code !== 'ENOENT' && !(error instanceof SyntaxError)) throw error; }
  }
  return result.sort();
}
module.exports = {detectLocal, prepareProfile, incompatibleExtensions};
