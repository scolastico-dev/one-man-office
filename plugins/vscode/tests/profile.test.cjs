'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');
const {detectLocal,prepareProfile}=require('../profile.cjs');
function fixture(t){
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-profile-'));
 t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 return {root,home:path.join(root,'home'),config:path.join(root,'config'),roaming:path.join(root,'roaming')};
}
for(const platform of ['linux','darwin','win32'])for(const [name,product,extensionRoot]of [
 ['Microsoft VS Code','Code','.vscode'],['VS Code Insiders','Code - Insiders','.vscode-insiders'],
 ['VSCodium','VSCodium','.vscode-oss'],['VSCodium Insiders','VSCodium - Insiders','.vscode-oss-insiders'],
 ['Code OSS','Code - OSS','.vscode-oss'],
])test(platform+' discovers '+name+' using its native profile and extension roots',t=>{
 const f=fixture(t),config=platform==='darwin'?path.join(f.home,'Library','Application Support'):platform==='win32'?f.roaming:f.config;
 const user=path.join(config,product,'User'),extensions=path.join(f.home,extensionRoot,'extensions');
 fs.mkdirSync(user,{recursive:true});fs.mkdirSync(extensions,{recursive:true});
 fs.writeFileSync(path.join(user,'settings.json'),'// local JSONC\n{"editor.fontSize":19,}\n');
 const selected=detectLocal({platform,home:f.home,env:{XDG_CONFIG_HOME:f.config,APPDATA:f.roaming}});
 assert.equal(selected.name,name);assert.equal(selected.user,user);assert.equal(selected.extensions,extensions);
});
test('standard home fallback and fixed preference select Microsoft before OSS without touching unused installations',t=>{
 const f=fixture(t);
 for(const product of ['Code','VSCodium'])fs.mkdirSync(path.join(f.home,'.config',product,'User'),{recursive:true});
 const found=detectLocal({platform:'linux',home:f.home,env:{}});
 assert.equal(found.name,'Microsoft VS Code');
 assert.equal(fs.existsSync(path.join(f.home,'.vscode','extensions')),false,'discovery is read-only');
 assert.equal(detectLocal({platform:'linux',home:path.join(f.root,'absent'),env:{}}),null);
});
test('preparation shares only desktop extensions and preference files, keeping editor databases and builtins private',t=>{
 const f=fixture(t),local={name:'VSCodium',user:path.join(f.root,'desktop','User'),extensions:path.join(f.root,'desktop','extensions')};
 fs.mkdirSync(local.user,{recursive:true});fs.writeFileSync(path.join(local.user,'settings.json'),'// desktop\n{"workbench.colorTheme":"Fixture"}');
 fs.writeFileSync(path.join(local.user,'state.vscdb'),'native encrypted secrets must remain local');
 const artifact=path.join(f.root,'artifact'),bundled=path.join(artifact,'extensions','vscode.fixture');
 fs.mkdirSync(bundled,{recursive:true});fs.writeFileSync(path.join(bundled,'package.json'),'{"name":"fixture"}');
 const profile=path.join(f.root,'profile'),state=path.join(f.root,'session');
 const result=prepareProfile({editor:'openvscode',root:artifact,profile,state,data:f.root,local,user:path.join(profile,'portable','user-data')});
 assert.equal(result.extensions,local.extensions);
 assert.equal(result.sharedUser,local.user);
 assert.ok(result.builtins.startsWith(state+path.sep));
 assert.equal(fs.readFileSync(path.join(result.builtins,'vscode.fixture','package.json'),'utf8'),'{"name":"fixture"}');
 const bridge=path.join(result.builtins,'omo.profile-bridge');
 assert.equal(JSON.parse(fs.readFileSync(path.join(bridge,'package.json'))).main,'./profile-bridge.cjs');
 const config=JSON.parse(fs.readFileSync(path.join(bridge,'profile.json')));
 assert.equal(config.browserUser.scheme,'vscode-userdata');assert.equal(config.browserUser.path,'/User');
 assert.equal(fs.readFileSync(path.join(local.user,'state.vscdb'),'utf8'),'native encrypted secrets must remain local');
 assert.deepEqual(fs.readdirSync(local.user).sort(),['settings.json','state.vscdb']);
});
test('desktop-only UI extensions are named as incompatible while themes, workspace and web extensions remain eligible',t=>{
 const f=fixture(t),extensions=path.join(f.root,'extensions');
 for(const [name,manifest]of Object.entries({
  'desktop-ui':{publisher:'fixture',name:'desktop-ui',main:'./index.js',extensionKind:['ui']},
  theme:{publisher:'fixture',name:'theme',contributes:{themes:[]}},
  workspace:{publisher:'fixture',name:'workspace',main:'./index.js',extensionKind:['workspace']},
  web:{publisher:'fixture',name:'web',main:'./index.js',browser:'./web.js',extensionKind:['ui']},
 })){
  fs.mkdirSync(path.join(extensions,name),{recursive:true});fs.writeFileSync(path.join(extensions,name,'package.json'),JSON.stringify(manifest));
 }
 const linked=path.join(f.root,'linked-extension');fs.mkdirSync(linked);fs.writeFileSync(path.join(linked,'package.json'),JSON.stringify({publisher:'fixture',name:'linked-ui',main:'index.js',extensionKind:['ui']}));fs.symlinkSync(linked,path.join(extensions,'linked'),'junction');
 assert.deepEqual(require('../profile.cjs').incompatibleExtensions(extensions),['fixture.desktop-ui','fixture.linked-ui']);
});
test('code-server preferences use the browser user-data provider rather than bypassing its configuration cache',t=>{
 const f=fixture(t),artifact=path.join(f.root,'artifact'),profile=path.join(f.root,'profile'),user=path.join(profile,'portable','user-data');fs.mkdirSync(artifact);fs.mkdirSync(path.join(user,'User'),{recursive:true});fs.writeFileSync(path.join(user,'User','settings.json'),'{}');
 const result=prepareProfile({editor:'code-server',root:artifact,profile,state:path.join(f.root,'session'),data:f.root,user,local:null});
 const config=JSON.parse(fs.readFileSync(path.join(result.builtins,'omo.profile-bridge','profile.json')));
 assert.equal(config.browserUser.scheme,'vscode-remote');assert.equal(config.browserUser.path,path.join(user,'User'));assert.equal(config.browserUser.native,true);
});
test('OpenVSCode renderer history lives in a private overlay without changing publisher bytes, even across an old file link',t=>{
 const f=fixture(t),artifact=path.join(f.root,'artifact'),profile=path.join(f.root,'profile'),state=path.join(f.root,'session');
 const relative=path.join('out','vs','code','browser','workbench','workbench.js'),original=path.join(artifact,relative);
 const content='// pinned renderer fixture: vscode-web-db vscode-userdata-store\n';fs.mkdirSync(path.dirname(original),{recursive:true});fs.writeFileSync(original,content);
 fs.writeFileSync(path.join(path.dirname(original),'workbench.html'),'publisher html');
 const options={editor:'openvscode',root:artifact,profile,state,data:f.root,user:path.join(profile,'portable','user-data'),local:null};
 prepareProfile(options);
 const overlay=path.join(state,'publisher',relative);
 assert.match(fs.readFileSync(overlay,'utf8'),/installBrowserHistory/);assert.equal(fs.readFileSync(original,'utf8'),content);
 fs.unlinkSync(overlay);fs.linkSync(original,overlay);
 prepareProfile(options);
 assert.equal(fs.readFileSync(original,'utf8'),content,'an old publisher hard link must never be written through');
 assert.match(fs.readFileSync(overlay,'utf8'),/installBrowserHistory/);
 assert.equal(fs.readFileSync(path.join(path.dirname(overlay),'workbench.html'),'utf8'),'publisher html');
});
for (const editor of ['code-server', 'openvscode']) test(editor + ' supplies desktop secrets through a private renderer overlay before extension startup', t => {
 const f=fixture(t),artifact=path.join(f.root,'artifact'),profile=path.join(f.root,'profile'),state=path.join(f.root,'session');
 const publisher=editor==='code-server'?path.join(artifact,'lib','vscode'):artifact;
 const relative=path.join('out','vs','code','browser','workbench','workbench.js'),original=path.join(publisher,relative);
 const content='// vscode-web-db vscode-userdata-store\ncreate({secretStorageProvider:e.remoteAuthority&&!t?void 0:new gjo(i)});';
 fs.mkdirSync(path.dirname(original),{recursive:true});fs.writeFileSync(original,content);
 const result=prepareProfile({editor,root:artifact,profile,state,data:f.root,user:path.join(profile,'portable','user-data'),local:null,secretCapability:'fixture-capability'});
 const overlaid=fs.readFileSync(path.join(state,'publisher',relative),'utf8');
 assert.match(overlaid,/fixture-capability/);assert.match(overlaid,/createSecretProvider/);
 assert.equal(fs.readFileSync(original,'utf8'),content);
 assert.ok(result.preload,'both editors must receive the overlay root preload');
});
