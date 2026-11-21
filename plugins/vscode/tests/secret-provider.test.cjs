'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const vm = require('node:vm');
const {SecretStore, installHTTP, patchRenderer} = require('../secret-provider.cjs');
const {createSecretProvider} = require('../browser-secrets.cjs');
function fixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'omo-secret-provider-'));
  t.after(() => fs.rmSync(directory, {recursive: true, force: true}));
  return directory;
}
test('imported secrets survive restart and concurrent folders; browser changes and sign-outs survive repeat import', t => {
  const directory = fixture(t), store = new SecretStore(directory);
  t.after(() => store.close());
  const imported = [{key: 'github', value: 'fixture-token'}, {key: 'other', value: 'second-token'}];
  store.import(imported, 'desktop');
  const peer = new SecretStore(directory); t.after(() => peer.close());
  assert.equal(peer.get('github'), 'fixture-token');
  store.set('github', 'browser-token'); store.delete('other');
  peer.import(imported, 'desktop');
  assert.equal(peer.get('github'), 'browser-token'); assert.equal(peer.get('other'), undefined);
  peer.import([{key: 'github', value: 'new-desktop-token'}], 'desktop');
  assert.equal(store.get('github'), 'browser-token', 'local refresh must preserve browser edits');
  store.import([{key: 'fresh', value: 'old'}], 'desktop');
  peer.import([{key: 'fresh', value: 'new'}], 'desktop');
  assert.equal(store.get('fresh'), 'new', 'unmodified imports follow desktop refresh');
  const files = fs.readdirSync(directory);
  for (const file of files) assert.ok(!fs.readFileSync(path.join(directory, file)).includes(Buffer.from('fixture-token')));
  if (process.platform !== 'win32') for (const file of files) assert.equal(fs.statSync(path.join(directory, file)).mode & 0o077, 0);
});
for (const base of ['/', '/proxy/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/']) {
  test(`browser provider uses the protected editor route at ${base} and survives a reload`, async t => {
    const directory = fixture(t), store = new SecretStore(directory); t.after(() => store.close());
    store.import([{key: 'github', value: 'fixture-session'}], 'desktop');
    const uninstall = installHTTP({directory, capability: 'private-capability', route: base + '_omo/secrets'}); t.after(uninstall);
    const server = http.createServer((req, res) => {res.statusCode = 404; res.end('publisher');});
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    t.after(() => new Promise(resolve => {server.closeAllConnections(); server.close(resolve);}));
    const url = `http://127.0.0.1:${server.address().port}${base}`;
    const request = (capability, method = 'POST', body = '{"action":"get","key":"github"}') => fetch(url + '_omo/secrets', {method, headers: {'Content-Type': 'application/json', 'X-Editor-Secret': capability}, ...(method === 'POST' ? {body} : {})});
    const denied = await request('wrong'); assert.equal(denied.status, 403); assert.doesNotMatch(await denied.text(), /fixture-session/);
    assert.equal((await request('private-capability', 'GET')).status, 405);
    const malformed = await request('private-capability', 'POST', '{"action":"set","key":"github","value":42}'); assert.equal(malformed.status, 400);
    const provider = createSecretProvider('private-capability', {fetch: (route, options) => fetch(new URL(route, url), options)});
    assert.equal(await provider.get('github'), 'fixture-session');
    await provider.set('new', 'browser-value');
    assert.deepEqual((await provider.keys()).sort(), ['github', 'new']);
    const reloaded = createSecretProvider('private-capability', {fetch: (route, options) => fetch(new URL(route, url), options)});
    assert.equal(await reloaded.get('new'), 'browser-value');
    await provider.delete('github'); assert.equal(await reloaded.get('github'), undefined);
    assert.equal((await request('private-capability')).headers.get('Cache-Control'), 'no-store');
  });
}
for (const selection of ['e.remoteAuthority&&!t?void 0:new gjo(i)', 'e.remoteStorageEnabled||e.remoteAuthority&&!t?void 0:new gjo(i)']) {
  test('renderer installs the provider before create() activates any extensions: ' + selection, async () => {
    const source = `const e={remoteAuthority:true},t=null,i=null;function gjo(){throw Error('publisher provider selected')} function create(options){globalThis.result=options.secretStorageProvider.get('github')} create({secretStorageProvider:${selection}});`;
    const context = {fetch: async () => ({ok: true, json: async () => ({value: 'fixture-session'})})};
    vm.runInNewContext(patchRenderer(source, 'private-capability'), context);
    assert.equal(await context.result, 'fixture-session');
  });
}
test('renderer fails closed on an unsupported publisher layout', () => {
  assert.throws(() => patchRenderer('create({differentProvider:true})', 'private-capability'), /unsupported publisher secret storage/);
});
