'use strict';
const fs = require('node:fs');
const path = require('node:path');
const {createHash, randomUUID} = require('node:crypto');
const {DatabaseSync} = require('node:sqlite');
const jsonc = require('./vendor/jsonc-parser/main.js');
const roots = ['settings.json', 'keybindings.json', 'tasks.json'];
const maxBytes = 1024 * 1024, maxFiles = 1024;
function allowed(name) {
  return roots.includes(name) || (name.startsWith('snippets/') && name.split('/').every(part => part && part !== '.' && part !== '..' && !part.includes('\\')));
}
function same(a, b) { return a === b || (a !== null && b !== null && a.equals(b)); }
function missing(error) { return error.code === 'ENOENT' || error.code === 'FileNotFound'; }
function checkedName(name) { if (!allowed(name)) throw new Error('unsupported preference file'); return name; }
function nativeFiles(root) {
  const file = name => path.join(root, ...checkedName(name).split('/'));
  return {
    async list() {
      const files = [];
      for (const name of roots) {
        try { if ((await fs.promises.stat(file(name))).isFile()) files.push(name); }
        catch (error) { if (!missing(error)) throw error; }
      }
      let visited = 0;
      async function walk(dir, ancestors = new Set()) {
        let real, entries;
        try { real = await fs.promises.realpath(path.join(root, dir)); if (ancestors.has(real)) return; ancestors = new Set([...ancestors, real]); if (++visited > maxFiles) throw new Error('too many preference directories'); entries = await fs.promises.readdir(real, {withFileTypes: true}); }
        catch (error) { if (missing(error)) return; throw error; }
        for (const entry of entries) {
          const name = dir + '/' + entry.name;
          if (!allowed(name)) continue;
          if (files.length >= maxFiles) throw new Error('too many preference files');
          let type = entry;
          if (entry.isSymbolicLink()) {
            try { type = await fs.promises.stat(file(name)); } catch (error) { if (missing(error) || error.code === 'ELOOP') continue; throw error; }
          }
          if (type.isDirectory()) await walk(name, ancestors);
          else if (type.isFile()) files.push(name);
        }
      }
      await walk('snippets'); return files;
    },
    async read(name) {
      try {
        const handle = await fs.promises.open(file(name), fs.constants.O_RDONLY | (fs.constants.O_NONBLOCK || 0));
        try {
          const stat = await handle.stat();
          if (!stat.isFile() || stat.size > maxBytes) throw new Error('unsupported preference file size or type');
          // Bound the read even if another program grows the file after stat.
          const value = Buffer.alloc(Math.min(stat.size + 1, maxBytes + 1));
          let count = 0;
          while (count < value.length) {
            const {bytesRead} = await handle.read(value, count, value.length - count, count);
            if (!bytesRead) return value.subarray(0, count);
            count += bytesRead;
          }
          throw new Error('preference file grew during its read');
        } finally { await handle.close(); }
      } catch (error) { if (missing(error)) return null; throw error; }
    },
    async write(name, value, expected) {
      let target = file(name);
      if (value === null) {
        if (expected !== undefined && !same(await this.read(name), expected)) return false;
        try { fs.unlinkSync(target); } catch (error) { if (!missing(error)) throw error; } return true;
      }
      if (value.length > maxBytes) throw new Error('preference file is too large');
      // Preserve dotfile-manager symlinks when changing an existing setting.
      try { target = await fs.promises.realpath(target); } catch (error) { if (!missing(error)) throw error; }
      await fs.promises.mkdir(path.dirname(target), {recursive: true, mode: 0o700});
      const temporary = target + '.omo-' + randomUUID();
      try {
        await fs.promises.writeFile(temporary, value, {mode: 0o600, flag: 'wx'});
        if (expected !== undefined && !same(await this.read(name), expected)) return false;
        fs.renameSync(temporary, target);
        return true;
      }
      finally { try { await fs.promises.unlink(temporary); } catch (error) { if (!missing(error)) throw error; } }
    },
  };
}
function parsed(name, value) {
  if (value === null) return null;
  const errors = [], result = jsonc.parse(value.toString('utf8').replace(/^\uFEFF/, ''), errors, {allowTrailingComma: true});
  if (errors.length || !result || typeof result !== 'object' || Array.isArray(result) !== (name === 'keybindings.json')) throw new Error('invalid preference JSONC');
  return result;
}
function editSetting(text, key, value) {
  const bom = text.startsWith('\uFEFF') ? '\uFEFF' : '';
  const body = text.slice(bom.length);
  return bom + jsonc.applyEdits(body, jsonc.modify(body, [key], value, {
    formattingOptions: {insertSpaces: true, tabSize: 2, eol: body.includes('\r\n') ? '\r\n' : '\n'},
  }));
}
const themeFields = ['workbench.colorTheme', 'workbench.preferredDarkColorTheme', 'workbench.preferredLightColorTheme',
  'workbench.preferredHighContrastColorTheme', 'workbench.preferredHighContrastLightColorTheme'];
const themeAliases = new Map([['Dark Modern', 'Default Dark Modern'], ['Light Modern', 'Default Light Modern'],
  ['Dark+', 'Default Dark+'], ['Light+', 'Default Light+']]);
function canonicalTheme(value) { return themeAliases.get(value) || value; }
function themeSettings(value, native) {
  if (value === null) return null;
  const settings = parsed('settings.json', value), existing = native === null ? null : parsed('settings.json', native);
  let text = value.toString('utf8');
  for (const key of themeFields) {
    if (!Object.hasOwn(settings, key)) continue;
    let desired = canonicalTheme(settings[key]);
    if (existing && Object.hasOwn(existing, key) && canonicalTheme(existing[key]) === desired) desired = existing[key];
    if (desired !== settings[key]) text = editSetting(text, key, desired);
  }
  return Buffer.from(text);
}
function equalValue(a, b) { return JSON.stringify(a) === JSON.stringify(b); }
function mergeSettings(base, desktop, browser) {
  const original = base === null ? {} : parsed('settings.json', base);
  const left = parsed('settings.json', desktop), right = parsed('settings.json', browser);
  let result = desktop.toString('utf8'), conflict = false;
  for (const key of new Set([...Object.keys(original), ...Object.keys(right)])) {
    if (equalValue(right[key], original[key])) continue;
    if (!equalValue(left[key], original[key]) && !equalValue(left[key], right[key])) { conflict = true; continue; }
    result = editSetting(result, key, right[key]);
  }
  return {value: Buffer.from(result), conflict};
}
class PreferenceSync {
  constructor({source, target, lock, backups, adoptBrowserOnce, onConflict = () => {}}) {
    this.adoptBrowserOnce = adoptBrowserOnce;
    this.source = source; this.target = target; this.backups = backups; this.onConflict = onConflict; this.previous = new Map(); this.initialized = false;
    fs.mkdirSync(path.dirname(lock), {recursive: true, mode: 0o700});
    this.database = new DatabaseSync(lock); this.database.exec('PRAGMA busy_timeout=0');
  }
  async preserve(name, ...values) {
    fs.mkdirSync(this.backups, {recursive: true, mode: 0o700});
    for (const value of values) {
      if (value === null) continue;
      const hash = createHash('sha256').update(name).update(value).digest('hex');
      try { fs.writeFileSync(path.join(this.backups, hash + '.jsonc'), value, {mode: 0o600, flag: 'wx'}); }
      catch (error) { if (error.code !== 'EEXIST') throw error; }
    }
    this.onConflict();
  }
  async tick() {
    try { this.database.exec('BEGIN IMMEDIATE'); }
    catch (error) { if (error.errcode === 5 || error.code === 'SQLITE_BUSY' || /database is locked/.test(error.message)) return false; throw error; }
    try {
      const adopt = !this.initialized && this.adoptBrowserOnce && !fs.existsSync(this.adoptBrowserOnce);
      let incomplete = false;
      const expectedVersions = new Map();
      const names = new Set([...(await this.source.list()), ...(await this.target.list()), ...this.previous.keys()]);
      if (names.size > maxFiles) throw new Error('too many preference files');
      for (const name of names) {
        checkedName(name);
        const rawBrowser = await this.target.read(name), rawDesktop = await this.source.read(name);
        // A file being edited may be temporarily invalid; retain the last good
        // snapshot and try again when its author finishes the JSONC document.
        try { parsed(name, rawDesktop); parsed(name, rawBrowser); } catch { incomplete = true; continue; }
        const browser = name === 'settings.json' ? themeSettings(rawBrowser, null) : rawBrowser;
        const desktop = name === 'settings.json' ? themeSettings(rawDesktop, null) : rawDesktop;
        const old = this.previous.has(name) ? this.previous.get(name) : this.initialized ? null : undefined;
        let result = desktop;
        if (old === undefined) {
          if (adopt && browser !== null) {
            result = browser;
            if (name === 'settings.json' && desktop !== null) {
              let text = browser.toString('utf8');
              const previous = parsed(name, browser), defaults = parsed(name, desktop);
              for (const key of Object.keys(defaults)) if (!Object.hasOwn(previous, key)) {
                text = editSetting(text, key, defaults[key]);
              }
              result = Buffer.from(text);
            }
            if (!same(desktop, result) && desktop !== null) await this.preserve(name, rawDesktop);
          } else if (!same(browser, desktop) && browser !== null) await this.preserve(name, rawBrowser);
        } else if (!same(browser, old) && !same(desktop, browser)) {
          if (same(desktop, old)) result = browser;
          else if (name === 'settings.json' && desktop !== null && browser !== null) {
            const merged = mergeSettings(old, desktop, browser); result = merged.value;
            if (merged.conflict) await this.preserve(name, rawDesktop, rawBrowser);
          } else await this.preserve(name, rawDesktop, rawBrowser);
        }
        // Editors do not take our transaction. Recheck both snapshots and
        // defer changed files without advancing their baseline or migration.
        expectedVersions.set(name, [result]);
        if (!same(await this.source.read(name), rawDesktop) || !same(await this.target.read(name), rawBrowser)) { incomplete = true; continue; }
        if (!same(desktop, result) && await this.source.write(name, name === 'settings.json' ? themeSettings(result, rawDesktop) : result, rawDesktop) === false) { incomplete = true; continue; }
        if (!same(browser, result) || (this.target.refreshOnSharedChanges && (old === undefined || !same(old, result)))) {
          if (!same(await this.target.read(name), rawBrowser) || await this.target.write(name, result, rawBrowser) === false) { incomplete = true; continue; }
        }
        this.previous.set(name, result);
      }
      // Provider writes can finish between our last read and a pending
      // mutation. Their durable history must be archived before acknowledgement;
      // the SQLite gate alone cannot serialize the user's editor saves.
      const history = await this.target.history?.() || [];
      for (const {name, value} of history) {
        checkedName(name);
        if (!(expectedVersions.get(name) || []).some(expected => same(expected, value))) await this.preserve(name, value);
      }
      await this.target.acknowledgeHistory?.(history);
      if (!incomplete) {
        this.initialized = true;
        if (adopt) {
          fs.mkdirSync(path.dirname(this.adoptBrowserOnce), {recursive: true, mode: 0o700});
          fs.writeFileSync(this.adoptBrowserOnce, '', {mode: 0o600});
        }
      }
      return !incomplete;
    } finally { this.database.exec('ROLLBACK'); }
  }
  close() { this.database.close(); }
}
module.exports = {PreferenceSync, nativeFiles, roots, allowed, maxBytes, maxFiles, missing};
