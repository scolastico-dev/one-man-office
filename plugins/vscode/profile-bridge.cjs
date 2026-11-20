'use strict';
const fs = require('node:fs');
const path = require('node:path');
const {randomUUID} = require('node:crypto');
const {PreferenceSync, nativeFiles, allowed, maxBytes, maxFiles, missing} = require('./preference-sync.cjs');
const {nativeHistory} = require('./preference-history.cjs');

function browserHistory(vscode, root) {
  const api = vscode.workspace.fs;
  const journal = vscode.Uri.joinPath(root, '.omo-preference-history.json');
  const acknowledgement = vscode.Uri.joinPath(root, '.omo-preference-ack.json');
  let ready;
  async function read() {
    const value = await api.readFile(journal);
    if (value.byteLength > 32 * 1024 * 1024) throw new Error('preference history is too large');
    const result = JSON.parse(Buffer.from(value).toString('utf8'));
    if (result.version !== 1 || !Array.isArray(result.entries)) throw new Error('unsupported preference history');
    return result;
  }
  const ensure = async () => {
    if (!ready) {
      const nonce = randomUUID();
      await api.createDirectory(root);
      await api.writeFile(acknowledgement, Buffer.from(JSON.stringify({through: 0, nonce})));
      if ((await read()).ready !== nonce) throw new Error('publisher preference history is unavailable');
      ready = true;
    }
  };
  return {
    ensure,
    async entries() {
      await ensure();
      return (await read()).entries.map(entry => {
        if (!allowed(entry.name) || !Number.isSafeInteger(entry.id) || typeof entry.value !== 'string' || entry.value.length > 32 * 1024 * 1024) throw new Error('invalid preference history record');
        return {...entry, value: Buffer.from(entry.value, 'base64')};
      });
    },
    async acknowledge(entries) {
      if (entries.length) await api.writeFile(acknowledgement, Buffer.from(JSON.stringify({through: Math.max(...entries.map(entry => entry.id))})));
    },
  };
}

// The remote extension host forwards these operations to the browser's actual
// user-data filesystem. Preserve its scheme/authority: a file URI can reach
// the same bytes while bypassing the browser's configuration change events.
// OpenVSCode's /User is not the server's Machine scope.
function browserFiles(vscode, root, history) {
  history ||= browserHistory(vscode, root);
  const api = vscode.workspace.fs;
  const equal = (a, b) => a === b || (a !== null && b !== null && a.equals(b));
  function resource(name) {
    if (!allowed(name)) throw new Error('unsupported preference file');
    return vscode.Uri.joinPath(root, ...name.split('/'));
  }
  return {
    history: () => history.entries(),
    acknowledgeHistory: entries => history.acknowledge(entries),
    // Native preferences are shared by several clients. Each workbench needs
    // its own provider operation event when another client changed the bytes.
    refreshOnSharedChanges: root.scheme === 'vscode-remote',
    async list() {
      const result = []; let visited = 0;
      async function walk(uri, prefix, ancestors = new Set()) {
        if (++visited > maxFiles || prefix.split('/').length > 64) throw new Error('too many preference directories');
        // code-server's remote provider exposes native paths; resolve directory
        // aliases for cycle detection. Browser-only providers have no symlinks.
        if (root.scheme === 'vscode-remote') {
          let real;
          try { real = await fs.promises.realpath(uri.fsPath || uri.path); } catch (error) { if (missing(error) || error.code === 'ELOOP') return; throw error; }
          if (ancestors.has(real)) return;
          ancestors = new Set([...ancestors, real]);
        }
        let entries;
        try { entries = await api.readDirectory(uri); } catch (error) { if (missing(error)) return; throw error; }
        for (const [name, type] of entries) {
          const relative = prefix + name;
          let kind = type;
          if ((kind & vscode.FileType.SymbolicLink) && !(kind & (vscode.FileType.File | vscode.FileType.Directory))) {
            try { kind = (await api.stat(vscode.Uri.joinPath(uri, name))).type; } catch (error) { if (missing(error) || error.code === 'ELOOP') continue; throw error; }
          }
          if (kind & vscode.FileType.Directory) {
            if (relative === 'snippets' || relative.startsWith('snippets/')) await walk(vscode.Uri.joinPath(uri, name), relative + '/', ancestors);
          } else if ((kind & vscode.FileType.File) && allowed(relative)) result.push(relative);
          if (result.length > maxFiles) throw new Error('too many preference files');
        }
      }
      await walk(root, ''); return result;
    },
    async read(name) {
      try {
        const value = await api.readFile(resource(name));
        if (value.byteLength > maxBytes) throw new Error('preference file is too large');
        return Buffer.from(value);
      } catch (error) { if (missing(error)) return null; throw error; }
    },
    async write(name, value, expected) {
      const uri = resource(name);
      await history.ensure?.();
      if (value === null) {
        if (expected !== undefined && !equal(await this.read(name), expected)) return false;
        if (expected === null && root.scheme === 'vscode-remote') {
          // A peer already removed this native file. Emit local provider events
          // as well, so this window drops its cached configuration. The journal
          // still protects a user save racing either of these operations.
          await api.createDirectory(vscode.Uri.joinPath(uri, '..'));
          await api.writeFile(uri, Buffer.from(name === 'keybindings.json' ? '[]' : '{}'));
        }
        try { await api.delete(uri, {recursive: false, useTrash: false}); } catch (error) { if (!missing(error)) throw error; } return; }
      if (value.length > maxBytes) throw new Error('preference file is too large');
      await api.createDirectory(vscode.Uri.joinPath(uri, '..'));
      if (expected !== undefined && !equal(await this.read(name), expected)) return false;
      await api.writeFile(uri, value);
      return true;
    },
  };
}
let finish;
async function activate(context) {
  const vscode = require('vscode');
  const config = JSON.parse(fs.readFileSync(path.join(context.extensionPath, 'profile.json'), 'utf8'));
  const browser = config.browserUser.native ? vscode.Uri.file(config.browserUser.path).with({scheme: config.browserUser.scheme}) : vscode.Uri.from(config.browserUser);
  const output = vscode.window.createOutputChannel('omo shared preferences');
  context.subscriptions.push(output);
  const sync = new PreferenceSync({
    source: nativeFiles(config.sharedUser), target: browserFiles(vscode, browser, config.nativeHistory ? nativeHistory(config.nativeHistory) : undefined), lock: config.lock,
    backups: config.backups, adoptBrowserOnce: config.adoptBrowserOnce,
    onConflict: () => output.appendLine('Different preference versions were preserved in ' + JSON.stringify(config.backups) + '.'),
  });
  let pending = false, draining, disposed = false, failed = false;
  function request() {
    pending = true;
    if (!draining) draining = (async () => {
      while (pending && !disposed) {
        pending = false;
        try { await sync.tick(); failed = false; }
        catch (error) {
          if (!failed) output.appendLine('Preference synchronization failed (' + String(error.code || error.name).replace(/[^A-Za-z0-9_]/g, '') + '). Check profile permissions and the editor version.');
          if (!failed && error.recoveryPath) output.appendLine('A native preference snapshot is retained at ' + JSON.stringify(error.recoveryPath) + '.');
          failed = true;
        }
      }
    })().finally(() => { draining = undefined; });
    return draining;
  }
  await request();
  output.appendLine('Shared preference files: ' + JSON.stringify(config.sharedUser));
  const timer = setInterval(request, 1000);
  context.subscriptions.push({dispose() { clearInterval(timer); }});
  context.subscriptions.push(vscode.workspace.onDidChangeConfiguration(request), vscode.workspace.onDidSaveTextDocument(request));
  finish = async () => {
    clearInterval(timer);
    await request();
    disposed = true;
    sync.close();
    finish = undefined;
  };
}
async function deactivate() { if (finish) await finish(); }
module.exports = {activate, deactivate, browserFiles, browserHistory};
