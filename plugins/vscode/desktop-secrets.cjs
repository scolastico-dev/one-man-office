'use strict';
// Read-only desktop SecretStorage import. Never return helper stderr/errors:
// native helpers and SQLite exceptions can contain credential material.
const fs = require('node:fs');
const path = require('node:path');
const {execFile} = require('node:child_process');
const {createDecipheriv, pbkdf2Sync} = require('node:crypto');
const {DatabaseSync} = require('node:sqlite');
const jsonc = require('./vendor/jsonc-parser/main.js');
const maxSecret = 1024 * 1024;

function runHelper(program, args, input) {
  return new Promise((resolve, reject) => {
    const child = execFile(program, args, {timeout: 15000, maxBuffer: 2 * maxSecret, windowsHide: true}, (error, stdout) => {
      if (error) reject(new Error('desktop keyring unavailable; check its native helper and unlock state'));
      else resolve(stdout);
    });
    child.stdin.on('error', () => {});
    child.stdin.end(input || '');
  });
}
// Electron configures safeStorage with app.getName(), which VS Code's build
// sets to product.nameShort (not the lowercase CLI applicationName).
const identities = {
  'Microsoft VS Code': 'Code',
  'VS Code Insiders': 'Code - Insiders',
  'VSCodium': 'VSCodium',
  'VSCodium Insiders': 'VSCodium - Insiders',
  'Code OSS': 'Code - OSS',
};
async function readDesktopSecrets(local, {platform = process.platform, env = process.env, run = runHelper} = {}) {
  if (!local) return [];
  const file = path.join(local.user, 'globalStorage', 'state.vscdb');
  if (!fs.existsSync(file)) return [];
  const product = identities[local.name];
  if (!product) throw new Error('unsupported desktop keyring identity');
  let selectedBackend;
  if (platform === 'linux' && local.extensions) {
    try {
      const argv = fs.readFileSync(path.join(local.extensions, '..', 'argv.json'), 'utf8');
      if (argv.length <= maxSecret) selectedBackend = jsonc.parse(argv)?.['password-store'];
    } catch (error) {if (error.code !== 'ENOENT') throw new Error('desktop keyring settings could not be read');}
  }
  let db, rows;
  try {
    db = new DatabaseSync(file, {readOnly: true});
    db.exec('PRAGMA busy_timeout=5000');
    rows = db.prepare("SELECT key, value FROM ItemTable WHERE key LIKE 'secret://%' LIMIT 4097").all();
    if (rows.length > 4096) throw new Error();
  } catch { throw new Error('desktop secret store could not be read'); }
  finally { db?.close(); }
  let password, windowsKey;
  async function kwalletPassword() {
    const versions = /^kwallet[56]?$/.test(selectedBackend || '') ? [selectedBackend.slice('kwallet'.length)] : ['6', '5', ''];
    for (const version of versions) {
      try {
        const reply = await run('gdbus', ['call', '--session', '--dest', 'org.kde.kwalletd' + version,
          '--object-path', '/modules/kwalletd' + version, '--method', 'org.kde.KWallet.networkWallet']);
        const match = /^\('([^'\r\n]+)',\)\s*$/.exec(reply);
        if (!match) throw new Error();
        return await run('kwallet-query', ['--read-password', 'Chromium Safe Storage', '--folder', 'Chromium Keys', match[1]]);
      } catch { /* Try the preceding KWallet service version. */ }
    }
    throw new Error('desktop keyring unavailable');
  }
  async function unprotect(buffer) {
    const script = '$ErrorActionPreference="Stop"; Add-Type -AssemblyName System.Security; ' +
      '$b=[Convert]::FromBase64String([Console]::In.ReadToEnd()); ' +
      '[Console]::Out.Write([Convert]::ToBase64String([Security.Cryptography.ProtectedData]::Unprotect($b,$null,[Security.Cryptography.DataProtectionScope]::CurrentUser)))';
    return Buffer.from(await run('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script], buffer.toString('base64')), 'base64');
  }
  async function decrypt(buffer) {
    if (platform === 'win32') {
      if (buffer.subarray(0, 3).toString() !== 'v10') return (await unprotect(buffer)).toString('utf8');
      if (!windowsKey) {
        const state = JSON.parse(fs.readFileSync(path.join(local.user, '..', 'Local State'), 'utf8'));
        const wrapped = Buffer.from(state.os_crypt.encrypted_key, 'base64');
        if (wrapped.subarray(0, 5).toString() !== 'DPAPI') throw new Error();
        windowsKey = await unprotect(wrapped.subarray(5));
      }
      const cipher = createDecipheriv('aes-256-gcm', windowsKey, buffer.subarray(3, 15));
      cipher.setAuthTag(buffer.subarray(-16));
      return Buffer.concat([cipher.update(buffer.subarray(15, -16)), cipher.final()]).toString('utf8');
    }
    const version = buffer.subarray(0, 3).toString();
    if (!(platform === 'linux' && ['v10', 'v11'].includes(version)) && !(platform === 'darwin' && version === 'v10')) throw new Error();
    let pass = 'peanuts';
    if (platform === 'darwin' || version === 'v11') {
      if (password === undefined) {
        const kde = typeof selectedBackend === 'string' ? /^kwallet[56]?$/.test(selectedBackend) : /KDE/i.test(env.XDG_CURRENT_DESKTOP || '');
        password = (await (platform === 'darwin'
          ? run('/usr/bin/security', ['find-generic-password', '-s', product + ' Safe Storage', '-a', product, '-w'])
          : kde ? kwalletPassword()
            : run('secret-tool', ['lookup', 'application', product]))).replace(/\r?\n$/, '');
        if (!password) throw new Error();
      }
      pass = password;
    }
    const key = pbkdf2Sync(pass, 'saltysalt', platform === 'darwin' ? 1003 : 1, 16, 'sha1');
    const cipher = createDecipheriv('aes-128-cbc', key, Buffer.alloc(16, 32));
    return Buffer.concat([cipher.update(buffer.subarray(3)), cipher.final()]).toString('utf8');
  }
  const records = [];
  try {
    for (const row of rows) {
      if (row.key.length > 8192 || row.value.length > 8 * maxSecret) throw new Error();
      const encoded = JSON.parse(Buffer.from(row.value).toString('utf8'));
      if (encoded.type !== 'Buffer' || !Array.isArray(encoded.data) || encoded.data.length > maxSecret || !encoded.data.every(n => Number.isInteger(n) && n >= 0 && n <= 255)) throw new Error();
      records.push({key: row.key.slice('secret://'.length), value: await decrypt(Buffer.from(encoded.data))});
    }
  } catch { throw new Error('desktop secret transfer failed; check the keyring helper, unlock state and desktop version'); }
  return records;
}
module.exports = {readDesktopSecrets};
