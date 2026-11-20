'use strict';
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const {DatabaseSync} = require('node:sqlite');
const {randomBytes, randomUUID, createCipheriv, createDecipheriv, createHmac, timingSafeEqual} = require('node:crypto');
const {createSecretProvider} = require('./browser-secrets.cjs');
const maxSecret = 1024 * 1024;

class SecretStore {
  constructor(directory) {
    fs.mkdirSync(directory, {recursive: true, mode: 0o700});
    const keyFile = path.join(directory, 'key'), temporary = keyFile + '.' + randomUUID();
    if (!fs.existsSync(keyFile)) {
      try {
        fs.writeFileSync(temporary, randomBytes(32), {flag: 'wx', mode: 0o600});
        try {fs.linkSync(temporary, keyFile);} catch (error) {if (error.code !== 'EEXIST') throw error;}
      } finally {fs.rmSync(temporary, {force: true});}
    }
    this.key = fs.readFileSync(keyFile);
    if (this.key.length !== 32) throw new Error('invalid editor secret key');
    const file = path.join(directory, 'secrets.sqlite');
    try {fs.writeFileSync(file, '', {flag: 'wx', mode: 0o600});} catch (error) {if (error.code !== 'EEXIST') throw error;}
    this.db = new DatabaseSync(file);
    this.db.exec('PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS secrets (key TEXT PRIMARY KEY, value BLOB); CREATE TABLE IF NOT EXISTS imports (source TEXT, key TEXT, digest TEXT, PRIMARY KEY (source,key))');
  }
  digest(value) {return value === undefined ? null : createHmac('sha256', this.key).update(value).digest('hex');}
  get(key) {
    const row = this.db.prepare('SELECT value FROM secrets WHERE key=?').get(key);
    if (!row?.value) return undefined;
    const value = Buffer.from(row.value), decipher = createDecipheriv('aes-256-gcm', this.key, value.subarray(0, 12));
    decipher.setAuthTag(value.subarray(12, 28));
    return Buffer.concat([decipher.update(value.subarray(28)), decipher.final()]).toString('utf8');
  }
  set(key, value) {
    if (typeof key !== 'string' || key.length > 8192 || typeof value !== 'string' || Buffer.byteLength(value) > maxSecret) throw new Error('invalid secret');
    const nonce = randomBytes(12), cipher = createCipheriv('aes-256-gcm', this.key, nonce);
    const encrypted = Buffer.concat([cipher.update(value, 'utf8'), cipher.final()]);
    this.db.prepare('INSERT OR REPLACE INTO secrets VALUES (?,?)').run(key, Buffer.concat([nonce, cipher.getAuthTag(), encrypted]));
  }
  delete(key) {this.db.prepare('INSERT OR REPLACE INTO secrets VALUES (?,NULL)').run(key);}
  keys() {return this.db.prepare('SELECT key FROM secrets WHERE value IS NOT NULL ORDER BY key').all().map(row => row.key);}
  import(records, source) {
    this.db.exec('BEGIN IMMEDIATE');
    try {
      for (const {key, value} of records) {
        const previous = this.db.prepare('SELECT digest FROM imports WHERE source=? AND key=?').get(source, key);
        const exists = this.db.prepare('SELECT 1 FROM secrets WHERE key=?').get(key);
        if (!exists || (previous && previous.digest === this.digest(this.get(key)))) this.set(key, value);
        this.db.prepare('INSERT OR REPLACE INTO imports VALUES (?,?,?)').run(source, key, this.digest(value));
      }
      this.db.exec('COMMIT');
    } catch (error) {this.db.exec('ROLLBACK'); throw error;}
  }
  close() {this.db.close(); this.key.fill(0);}
}

// The publisher owns its listener. Intercept only this exact private route,
// before its router can serve files; normal HTTP/WebSockets remain untouched.
// The capability is present in the authenticated renderer, never in a URL.
function installHTTP({directory, capability, route}) {
  const original = http.Server.prototype.emit;
  let store;
  function emit(event, req, res, ...rest) {
    if (event !== 'request' || req.url?.split('?')[0] !== route) return original.call(this, event, req, res, ...rest);
    const reply = (status, value) => {
      res.writeHead(status, {'Content-Type': 'application/json', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff'});
      res.end(JSON.stringify(value));
    };
    const received = Buffer.from(String(req.headers['x-editor-secret'] || '')), expected = Buffer.from(capability);
    if (received.length !== expected.length || !timingSafeEqual(received, expected)) {req.resume(); reply(403, {}); return true;}
    if (req.method !== 'POST') {req.resume(); reply(405, {}); return true;}
    if (req.headers['content-type'] !== 'application/json') {req.resume(); reply(415, {}); return true;}
    let bytes = 0, chunks = [], oversized = false;
    req.on('data', chunk => {
      bytes += chunk.length;
      if (bytes > 2 * maxSecret) {oversized = true; chunks = [];}
      else if (!oversized) chunks.push(chunk);
    });
    req.on('error', () => {});
    req.on('end', () => {
      if (oversized) {reply(413, {}); return;}
      let body;
      try {
        body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        if (!body || !['get', 'set', 'delete', 'keys'].includes(body.action) ||
          (body.action !== 'keys' && (typeof body.key !== 'string' || body.key.length > 8192)) ||
          (body.action === 'set' && (typeof body.value !== 'string' || Buffer.byteLength(body.value) > maxSecret))) throw new Error();
      } catch {reply(400, {}); return;}
      try {
        store ||= new SecretStore(directory);
        const value = body.action === 'keys' ? store.keys() : body.action === 'get' ? store.get(body.key) :
          body.action === 'set' ? store.set(body.key, body.value) : store.delete(body.key);
        reply(200, {value});
      } catch {reply(503, {});}
    });
    return true;
  }
  http.Server.prototype.emit = emit;
  return () => {http.Server.prototype.emit = original; store?.close();};
}
function patchRenderer(source, capability) {
  // Both pinned renderers construct the embedder's provider in one place.
  // Match the complete conditional, including code-server's remote setting;
  // refuse unknown builds rather than leaving authentication in memory.
  const pattern = /secretStorageProvider:[\w$]+\.(?:remoteStorageEnabled\|\|[\w$]+\.)?remoteAuthority&&![\w$]+\?void 0:new [\w$]+\([\w$]+\)/g;
  const matches = source.match(pattern);
  if (matches?.length !== 1) throw new Error('unsupported publisher secret storage');
  return source.replace(pattern, 'secretStorageProvider:(' + createSecretProvider.toString() + ')(' + JSON.stringify(capability) + ')');
}
module.exports = {SecretStore, installHTTP, patchRenderer};
