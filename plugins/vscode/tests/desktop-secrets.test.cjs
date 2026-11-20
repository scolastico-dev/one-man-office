'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {DatabaseSync} = require('node:sqlite');
const {createCipheriv, pbkdf2Sync} = require('node:crypto');
const {readDesktopSecrets} = require('../desktop-secrets.cjs');

// A real SQLite store and independently encrypted Chromium-format records.
// A wrong keyring identity, iteration count, prefix or query breaks these tests.
function fixture(t, platform, version = 'v11') {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'omo-desktop-secrets-'));
  t.after(() => fs.rmSync(root, {recursive: true, force: true}));
  const user = path.join(root, 'Code', 'User'), file = path.join(user, 'globalStorage', 'state.vscdb');
  fs.mkdirSync(path.dirname(file), {recursive: true});
  const db = new DatabaseSync(file);
  db.exec('CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value BLOB)');
  const key = '{"extensionId":"vscode.github-authentication","key":"github.auth"}';
  const value = '[{"id":"session","account":{"id":"1","label":"fixture"},"scopes":["repo"],"accessToken":"fixture-token"}]';
  const password = platform === 'linux' && version === 'v10' ? 'peanuts' : 'fixture-keyring-password';
  const cipher = createCipheriv('aes-128-cbc', pbkdf2Sync(password, 'saltysalt', platform === 'darwin' ? 1003 : 1, 16, 'sha1'), Buffer.alloc(16, 32));
  const encrypted = Buffer.concat([Buffer.from(version), cipher.update(value), cipher.final()]);
  db.prepare('INSERT INTO ItemTable VALUES (?, ?)').run('secret://' + key, JSON.stringify(encrypted));
  db.prepare('INSERT INTO ItemTable VALUES (?, ?)').run('unrelated-setting', 'must not import');
  db.close();
  return {root, user, file, key, value, encrypted, local: {name: 'Microsoft VS Code', user}};
}
for (const [platform, version] of [['linux', 'v10'], ['linux', 'v11'], ['darwin', 'v10']]) {
  test(`${platform} ${version} transfers decrypted GitHub sessions without changing the desktop database`, async t => {
    const f = fixture(t, platform, version), before = fs.readFileSync(f.file), calls = [];
    const records = await readDesktopSecrets(f.local, {platform, run: async (program, args) => {
      calls.push([program, args]); return 'fixture-keyring-password\n';
    }});
    assert.deepEqual(records, [{key: f.key, value: f.value}]);
    assert.deepEqual(fs.readFileSync(f.file), before);
    if (version === 'v10' && platform === 'linux') assert.deepEqual(calls, []);
    else if (platform === 'linux') assert.deepEqual(calls, [['secret-tool', ['lookup', 'application', 'Code']]]);
    else assert.deepEqual(calls, [['/usr/bin/security', ['find-generic-password', '-s', 'Code Safe Storage', '-a', 'Code', '-w']]]);
  });
}
test('missing desktop store imports nothing and creates no database', async t => {
  const f = fixture(t, 'linux'); fs.unlinkSync(f.file);
  assert.deepEqual(await readDesktopSecrets(f.local), []);
  assert.equal(fs.existsSync(f.file), false);
});
test('a failed keyring lookup exposes no helper output or encrypted record and leaves the source intact', async t => {
  const f = fixture(t, 'linux'), before = fs.readFileSync(f.file);
  await assert.rejects(readDesktopSecrets(f.local, {platform: 'linux', run: async () => {throw new Error('fixture-token keyring-password');}}), error => {
    assert.doesNotMatch(String(error), /fixture-token|keyring-password|Buffer|data/);
    return /keyring/.test(error.message);
  });
  assert.deepEqual(fs.readFileSync(f.file), before);
});
test('KWallet uses the product safe-storage entry in the configured network wallet', async t => {
  const f = fixture(t, 'linux'), calls = [];
  assert.deepEqual(await readDesktopSecrets(f.local, {platform: 'linux', env: {XDG_CURRENT_DESKTOP: 'KDE'}, run: async (program, args) => {
    calls.push([program, args]); return program === 'gdbus' ? "('custom-wallet',)\n" : 'fixture-keyring-password\n';
  }}), [{key: f.key, value: f.value}]);
  assert.deepEqual(calls, [
    ['gdbus', ['call', '--session', '--dest', 'org.kde.kwalletd6', '--object-path', '/modules/kwalletd6', '--method', 'org.kde.KWallet.networkWallet']],
    ['kwallet-query', ['--read-password', 'Chromium Safe Storage', '--folder', 'Chromium Keys', 'custom-wallet']],
  ]);
});
test('an explicit desktop password-store setting takes precedence over the current desktop environment',async t=>{
 const f=fixture(t,'linux');f.local.extensions=path.join(f.root,'.vscode','extensions');
 fs.mkdirSync(path.dirname(f.local.extensions));
 fs.writeFileSync(path.join(f.root,'.vscode','argv.json'),'// Chosen by the desktop editor\n{"password-store":"gnome-libsecret",}\n');
 assert.deepEqual(await readDesktopSecrets(f.local,{platform:'linux',env:{XDG_CURRENT_DESKTOP:'KDE'},run:async(program,args)=>{
   assert.deepEqual([program,args],['secret-tool',['lookup','application','Code']]);return 'fixture-keyring-password\n';
 }}),[{key:f.key,value:f.value}]);
});
for(const [backend,version]of [['kwallet5','5'],['kwallet','']])test('explicit '+backend+' selects its own key when a newer wallet service also exists',async t=>{
 const f=fixture(t,'linux');f.local.extensions=path.join(f.root,'.vscode','extensions');fs.mkdirSync(path.dirname(f.local.extensions));
 fs.writeFileSync(path.join(f.root,'.vscode','argv.json'),JSON.stringify({'password-store':backend}));
 const services=[];
 assert.deepEqual(await readDesktopSecrets(f.local,{platform:'linux',env:{XDG_CURRENT_DESKTOP:'KDE'},run:async(program,args)=>{
   if(program==='gdbus'){services.push(args[3]);return args[3]==='org.kde.kwalletd'+version?"('configured-wallet',)\n":"('other-wallet',)\n";}
   return args.at(-1)==='configured-wallet'?'fixture-keyring-password\n':'different-native-key\n';
 }}),[{key:f.key,value:f.value}]);
 assert.deepEqual(services,['org.kde.kwalletd'+version]);
});
test('native source enables bounded SQLite lock waiting before reading a live desktop store',async t=>{
 const f=fixture(t,'linux','v10'),prepare=DatabaseSync.prototype.prepare;let inspected=false;
 DatabaseSync.prototype.prepare=function(sql,...args){
  if(sql.startsWith('SELECT key, value FROM ItemTable')){
   // Observe the real driver connection policy at the query boundary. This
   // avoids a timing-based test racing an exclusive lock against its release.
   const timeout=prepare.call(this,'PRAGMA busy_timeout').get().timeout;
   assert.ok(timeout>0&&timeout<=15000,'live desktop reads must wait for short writer transactions');inspected=true;
  }
  return prepare.call(this,sql,...args);
 };
 t.after(()=>{DatabaseSync.prototype.prepare=prepare;});
 assert.deepEqual(await readDesktopSecrets(f.local,{platform:'linux'}),[{key:f.key,value:f.value}]);assert.equal(inspected,true);
});
test('Windows DPAPI ciphertext is passed privately on stdin, never in arguments', async t => {
  const f = fixture(t, 'linux'), db = new DatabaseSync(f.file);
  db.prepare('UPDATE ItemTable SET value=? WHERE key=?').run(JSON.stringify(Buffer.from('dpapi-fixture')), 'secret://' + f.key); db.close();
  const records = await readDesktopSecrets(f.local, {platform: 'win32', run: async (program, args, input) => {
    assert.match(program, /powershell/i); assert.ok(args.includes('-NonInteractive'));
    assert.ok(!args.join(' ').includes('ZHBhcGktZml4dHVyZQ=='));
    assert.equal(input, 'ZHBhcGktZml4dHVyZQ=='); return Buffer.from(f.value).toString('base64');
  }});
  assert.deepEqual(records, [{key: f.key, value: f.value}]);
});
test('Windows profile-bound AES-GCM decrypts its DPAPI-wrapped Local State key', async t => {
  const f=fixture(t,'linux'),key=Buffer.alloc(32,7),nonce=Buffer.alloc(12,3);
  const cipher=createCipheriv('aes-256-gcm',key,nonce),encrypted=Buffer.concat([cipher.update(f.value),cipher.final()]);
  fs.writeFileSync(path.join(f.user,'..','Local State'),JSON.stringify({os_crypt:{encrypted_key:Buffer.from('DPAPIwrapped-key').toString('base64')}}));
  const db=new DatabaseSync(f.file);db.prepare('UPDATE ItemTable SET value=? WHERE key=?').run(JSON.stringify(Buffer.concat([Buffer.from('v10'),nonce,encrypted,cipher.getAuthTag()])), 'secret://'+f.key);db.close();
  assert.deepEqual(await readDesktopSecrets(f.local,{platform:'win32',run:async(program,args,input)=>{
    assert.equal(input,Buffer.from('wrapped-key').toString('base64'));return key.toString('base64');
  }}),[{key:f.key,value:f.value}]);
});
for(const [name,application,product] of [
  ['VS Code Insiders','code-insiders','Code - Insiders'],['VSCodium','codium','VSCodium'],
  ['VSCodium Insiders','codium-insiders','VSCodium - Insiders'],['Code OSS','code-oss','Code - OSS'],
])for(const platform of ['linux','darwin'])test(platform+' reads the product-specific keyring identity for '+name,async t=>{
  const f=fixture(t,platform,platform==='darwin'?'v10':'v11');f.local.name=name;
  assert.deepEqual(await readDesktopSecrets(f.local,{platform,env:{},run:async(program,args)=>{
    if(platform==='linux')assert.deepEqual([program,args],['secret-tool',['lookup','application',product]]);
    else assert.deepEqual([program,args],['/usr/bin/security',['find-generic-password','-s',product+' Safe Storage','-a',product,'-w']]);
    return 'fixture-keyring-password\n';
  }}),[{key:f.key,value:f.value}]);
});
