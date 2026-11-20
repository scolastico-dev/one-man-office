'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');
const {PreferenceSync,nativeFiles}=require('../preference-sync.cjs');
function fixture(t){
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-preferences-')),engines=[];
 const source=path.join(root,'desktop'),browser=path.join(root,'browser');
 fs.mkdirSync(source);fs.mkdirSync(browser);
 t.after(()=>{for(const engine of engines)engine.close();fs.rmSync(root,{recursive:true,force:true});});
 const put=(dir,name,text)=>{const file=path.join(dir,name);fs.mkdirSync(path.dirname(file),{recursive:true});fs.writeFileSync(file,text);};
 const get=(dir,name)=>fs.readFileSync(path.join(dir,name),'utf8');
 const make=(target=nativeFiles(browser))=>{const sync=new PreferenceSync({source:nativeFiles(source),target,lock:path.join(root,'lock.sqlite'),backups:path.join(root,'conflicts')});engines.push(sync);return sync;};
 return {root,source,browser,put,get,make};
}
for(const name of ['settings.json','keybindings.json','tasks.json','snippets/shared.json'])for(const mutation of ['write','delete'])test(`${name}: each native-profile window observes a shared ${mutation} already completed by another bridge`,async t=>{
 const f=fixture(t),original=name==='keybindings.json'?'[]':'{"a":1}',changed=name==='keybindings.json'?'[{"key":"ctrl+k","command":"fixture"}]':'{"a":2}';f.put(f.source,name,original);
 const caches=[original,original],files=nativeFiles(f.browser),engines=[];
 for(let client=0;client<2;client++)engines.push(f.make({...files,refreshOnSharedChanges:true,write:async(...args)=>{const result=await files.write(...args);if(result!==false)caches[client]=args[1]?.toString()??null;return result;}}));
 for(const engine of engines)await engine.tick();
 if(mutation==='write')f.put(f.source,name,changed);else fs.unlinkSync(path.join(f.source,name));
 // A deterministic completion order exposes the cache bug: client two reads
 // bytes that client one has already updated, but must still notify its UI.
 for(const engine of engines)await engine.tick();
 assert.deepEqual(caches,[mutation==='write'?changed:null,mutation==='write'?changed:null]);
});
test('initial import applies JSONC preferences and snippets without copying native storage or secrets',async t=>{
 const f=fixture(t),settings='// theme from desktop\n{"workbench.colorTheme":"Fixture Dark",}\n';
 f.put(f.source,'settings.json',settings);f.put(f.source,'keybindings.json','[{"key":"ctrl+k","command":"fixture"}]');f.put(f.source,'snippets/fixture.code-snippets','{"Fixture":{"prefix":"fixture","body":"ok"}}');
 f.put(f.source,'globalStorage/state.vscdb','native secrets');f.put(f.source,'workspaceStorage/workspace/state.vscdb','workspace state');
 f.put(f.browser,'globalStorage/fixture/state.json','browser state');
 await f.make().tick();
 assert.equal(f.get(f.browser,'settings.json'),settings);assert.equal(f.get(f.browser,'keybindings.json'),'[{"key":"ctrl+k","command":"fixture"}]');
 assert.equal(f.get(f.browser,'snippets/fixture.code-snippets'),' {"Fixture":{"prefix":"fixture","body":"ok"}}'.trim());
 assert.equal(fs.existsSync(path.join(f.browser,'globalStorage/state.vscdb')),false);
 assert.equal(fs.existsSync(path.join(f.browser,'workspaceStorage')),false);
 assert.equal(f.get(f.browser,'globalStorage/fixture/state.json'),'browser state');
});
test('edits and removals flow in both directions and survive a new browser origin',async t=>{
 const f=fixture(t);f.put(f.source,'settings.json','{"editor.fontSize":18}');const sync=f.make();await sync.tick();
 f.put(f.source,'settings.json','// desktop changed\n{"editor.fontSize":20}');await sync.tick();assert.equal(f.get(f.browser,'settings.json'),'// desktop changed\n{"editor.fontSize":20}');
 f.put(f.browser,'settings.json','// browser changed\n{"editor.fontSize":22}');f.put(f.browser,'snippets/new.json','{"snippet":{"prefix":"n","body":"new"}}');await sync.tick();
 assert.equal(f.get(f.source,'settings.json'),'// browser changed\n{"editor.fontSize":22}');assert.ok(fs.existsSync(path.join(f.source,'snippets/new.json')));
 fs.unlinkSync(path.join(f.browser,'snippets/new.json'));await sync.tick();assert.equal(fs.existsSync(path.join(f.source,'snippets/new.json')),false);
 const next=path.join(f.root,'new-origin');fs.mkdirSync(next);await f.make(nativeFiles(next)).tick();assert.equal(f.get(next,'settings.json'),'// browser changed\n{"editor.fontSize":22}');
 fs.unlinkSync(path.join(f.source,'settings.json'));await sync.tick();assert.equal(fs.existsSync(path.join(f.browser,'settings.json')),false);
});
test('concurrent disjoint settings merge without removing comments or either change',async t=>{
 const f=fixture(t);f.put(f.source,'settings.json','// retain this comment\n{"editor.fontSize":18,"editor.minimap.enabled":true,}');const sync=f.make();await sync.tick();
 f.put(f.source,'settings.json','// retain this comment\n{"editor.fontSize":20,"editor.minimap.enabled":true,}');
 f.put(f.browser,'settings.json','{"editor.fontSize":18,"editor.minimap.enabled":false}');await sync.tick();
 const result=f.get(f.source,'settings.json');assert.match(result,/retain this comment/);assert.deepEqual(require('../vendor/jsonc-parser/main.js').parse(result),{'editor.fontSize':20,'editor.minimap.enabled':false});assert.equal(f.get(f.browser,'settings.json'),result);
});
test('a same-setting conflict keeps the desktop value and privately preserves both complete versions',async t=>{
 const f=fixture(t);f.put(f.source,'settings.json','{"editor.fontSize":18}');const sync=f.make();await sync.tick();
 const desktop='// desktop\n{"editor.fontSize":20}',browser='// browser\n{"editor.fontSize":22}';f.put(f.source,'settings.json',desktop);f.put(f.browser,'settings.json',browser);await sync.tick();
 assert.equal(require('../vendor/jsonc-parser/main.js').parse(f.get(f.source,'settings.json'))['editor.fontSize'],20);
 const preserved=fs.readdirSync(path.join(f.root,'conflicts')).map(name=>f.get(path.join(f.root,'conflicts'),name));assert.ok(preserved.includes(desktop));assert.ok(preserved.includes(browser));
});
test('a partially written JSONC file never replaces valid browser preferences',async t=>{
 const f=fixture(t);f.put(f.source,'settings.json','{"editor.fontSize":18}');const sync=f.make();await sync.tick();f.put(f.source,'settings.json','{"editor.fontSize":');await sync.tick();assert.equal(f.get(f.browser,'settings.json'),'{"editor.fontSize":18}');
 f.put(f.source,'settings.json','{"editor.fontSize":24}');await sync.tick();assert.equal(f.get(f.browser,'settings.json'),'{"editor.fontSize":24}');
});
test('two browser instances serialize writes and merge disjoint edits rather than dropping one',async t=>{
 const f=fixture(t);f.put(f.source,'settings.json','{"editor.fontSize":18,"editor.minimap.enabled":true}');
 const files=nativeFiles(f.browser);let paused=false,arrived,release;const entered=new Promise(r=>arrived=r),resume=new Promise(r=>release=r);
 const first=f.make({...files,read:async name=>{if(paused&&name==='settings.json'){arrived();await resume;}return files.read(name);}});
 await first.tick();const other=path.join(f.root,'other-browser');fs.mkdirSync(other);const second=f.make(nativeFiles(other));await second.tick();
 f.put(f.browser,'settings.json','{"editor.fontSize":20,"editor.minimap.enabled":true}');f.put(other,'settings.json','{"editor.fontSize":18,"editor.minimap.enabled":false}');
 paused=true;const pending=first.tick();await entered;
 try{assert.equal(await second.tick(),false,'the second bridge must defer while the first holds the transaction');}finally{paused=false;release();}
 await pending;await second.tick();await first.tick();assert.deepEqual(require('../vendor/jsonc-parser/main.js').parse(f.get(f.source,'settings.json')),{'editor.fontSize':20,'editor.minimap.enabled':false});assert.equal(f.get(f.browser,'settings.json'),f.get(other,'settings.json'));
});
test('first no-desktop migration keeps existing browser choices and thereafter trusts the persistent copy',async t=>{
 const f=fixture(t),marker=path.join(f.root,'imported');f.put(f.source,'settings.json','{"workbench.colorTheme":"Default Dark Modern","chat.disableAIFeatures":true}');f.put(f.browser,'settings.json','// existing browser choice\n{"workbench.colorTheme":"Existing Light","editor.fontSize":21}');
 const sync=new PreferenceSync({source:nativeFiles(f.source),target:nativeFiles(f.browser),lock:path.join(f.root,'lock.sqlite'),backups:path.join(f.root,'conflicts'),adoptBrowserOnce:marker});
 try {await sync.tick();const result=require('../vendor/jsonc-parser/main.js').parse(f.get(f.source,'settings.json'));assert.deepEqual(result,{'workbench.colorTheme':'Existing Light','editor.fontSize':21,'chat.disableAIFeatures':true});assert.match(f.get(f.source,'settings.json'),/existing browser choice/);assert.ok(fs.existsSync(marker));}
 finally{sync.close();}
 const next=path.join(f.root,'next');fs.mkdirSync(next);f.put(next,'settings.json','{}');const restarted=new PreferenceSync({source:nativeFiles(f.source),target:nativeFiles(next),lock:path.join(f.root,'lock.sqlite'),backups:path.join(f.root,'conflicts'),adoptBrowserOnce:marker});
 try{await restarted.tick();assert.equal(f.get(next,'settings.json'),f.get(f.source,'settings.json'));}finally{restarted.close();}
});
test('a short filesystem read is completed before preferences are parsed or copied',async t=>{
 const f=fixture(t),text='{"editor.fontSize":19,"workbench.colorTheme":"Default Dark Modern"}';f.put(f.source,'settings.json',text);
 const open=fs.promises.open.bind(fs.promises);
 t.mock.method(fs.promises,'open',async(...args)=>{const handle=await open(...args),read=handle.read.bind(handle);handle.read=(buffer,offset,length,position)=>read(buffer,offset,Math.min(length,3),position);return handle;});
 const value=await nativeFiles(f.source).read('settings.json');assert.equal(value.toString(),text);
});
test('a process exiting while holding the shared SQLite gate cannot strand future synchronization',async t=>{
 const f=fixture(t),{spawn}=require('node:child_process'),{once}=require('node:events');
 f.put(f.source,'settings.json','{"editor.fontSize":19}');const sync=f.make();
 const child=spawn(process.execPath,['-e',"const {DatabaseSync}=require('node:sqlite');const db=new DatabaseSync(process.argv[1]);db.exec('BEGIN IMMEDIATE');console.log('locked');process.stdin.resume();",path.join(f.root,'lock.sqlite')],{env:{PATH:process.env.PATH},stdio:['pipe','pipe','pipe']});
 const closed=once(child,'close');
 try{
  await new Promise((resolve,reject)=>{let text='';child.on('error',reject);child.stdout.on('data',value=>{text+=value;if(text.includes('locked\n'))resolve();});child.on('close',()=>reject(new Error('lock holder exited before announcing its transaction')));});
  assert.equal(await sync.tick(),false);child.kill('SIGTERM');await closed;
  assert.equal(await sync.tick(),true);assert.equal(f.get(f.browser,'settings.json'),'{"editor.fontSize":19}');
 }finally{if(child.exitCode===null&&child.signalCode===null){child.kill('SIGTERM');await closed;}}
});
test('browser normalization of built-in theme IDs never rewrites desktop theme choices or loops settings writes',async t=>{
 const f=fixture(t),original='// desktop theme ID\n{"workbench.colorTheme":"Default Light Modern","workbench.preferredDarkColorTheme":"Default Dark Modern","editor.fontSize":18}';f.put(f.source,'settings.json',original);const sync=f.make();await sync.tick();
 const browser='// desktop theme ID\n{"workbench.colorTheme":"Light Modern","workbench.preferredDarkColorTheme":"Dark Modern","editor.fontSize":18}';f.put(f.browser,'settings.json',browser);await sync.tick();
 assert.equal(f.get(f.source,'settings.json'),original);assert.equal(f.get(f.browser,'settings.json'),browser);
 f.put(f.browser,'settings.json',browser.replace('18','22'));await sync.tick();const actual=require('../vendor/jsonc-parser/main.js').parse(f.get(f.source,'settings.json'));assert.equal(actual['editor.fontSize'],22);assert.equal(actual['workbench.colorTheme'],'Default Light Modern');assert.equal(actual['workbench.preferredDarkColorTheme'],'Default Dark Modern');
});
test('UTF-8 BOM and CRLF JSONC preferences retain their encoding marker and comments through browser edits',async t=>{
 const f=fixture(t),text='\uFEFF// desktop\r\n{"editor.fontSize":18,"workbench.colorTheme":"Default Light Modern",}\r\n';f.put(f.source,'settings.json',text);const sync=f.make();await sync.tick();assert.equal(f.get(f.browser,'settings.json'),text);
 f.put(f.browser,'settings.json',text.replace('18','22'));await sync.tick();assert.equal(f.get(f.source,'settings.json'),text.replace('18','22'));
});
for (const name of ['settings.json','snippets/race.json']) {
 for (const during of ['source recheck','source write']) test(`${name}: browser saves during ${during} survive the tick and subsequent merge`,async t=>{
  const f=fixture(t),original='{"a":1}',desktop='{"a":2}',browser='{"a":3}';f.put(f.source,name,original);
  const source=nativeFiles(f.source);let armed=false,reads=0;
  const sync=new PreferenceSync({source:{...source,read:async file=>{const value=await source.read(file);if(armed&&file===name&&++reads===2&&during==='source recheck')f.put(f.browser,name,browser);return value;},write:async(...args)=>{if(armed&&during==='source write')f.put(f.browser,name,browser);return source.write(...args);}},target:nativeFiles(f.browser),lock:path.join(f.root,'lock.sqlite'),backups:path.join(f.root,'conflicts')});t.after(()=>sync.close());
  await sync.tick();
  if(during==='source write') f.put(f.browser,name,'{"a":2}'); else f.put(f.source,name,desktop);
  armed=true;await sync.tick();armed=false;
  assert.equal(f.get(f.browser,name),browser);await sync.tick();
  const versions=[f.get(f.source,name),f.get(f.browser,name),...(fs.existsSync(path.join(f.root,'conflicts'))?fs.readdirSync(path.join(f.root,'conflicts')).map(file=>f.get(path.join(f.root,'conflicts'),file)):[])];
  assert.ok(versions.includes(browser),'the browser edit must remain live or preserved');
 });
}
test('an edit during initial import defers initialization and no-desktop migration',async t=>{
 const f=fixture(t),target=nativeFiles(f.browser),source=nativeFiles(f.source),marker=path.join(f.root,'imported');f.put(f.source,'settings.json','{"a":1}');let armed=true;
 const sync=new PreferenceSync({source:{...source,read:async name=>{const value=await source.read(name);if(armed){armed=false;f.put(f.source,name,'{"a":2}');}return value;}},target,lock:path.join(f.root,'lock.sqlite'),backups:path.join(f.root,'conflicts'),adoptBrowserOnce:marker});t.after(()=>sync.close());
 assert.equal(await sync.tick(),false);assert.equal(sync.initialized,false);assert.equal(fs.existsSync(marker),false);await sync.tick();assert.equal(f.get(f.browser,'settings.json'),'{"a":2}');
});
test('linked snippet files and directories synchronize both ways without replacing links or following cycles',async t=>{
 const f=fixture(t),dotfiles=path.join(f.root,'dotfiles');fs.mkdirSync(dotfiles);f.put(dotfiles,'linked.json','{"a":1}');f.put(dotfiles,'nested/deep.json','{"b":1}');fs.mkdirSync(path.join(f.source,'snippets'));
 fs.symlinkSync(path.join(dotfiles,'linked.json'),path.join(f.source,'snippets/linked.json'),'file');fs.symlinkSync(path.join(dotfiles,'nested'),path.join(f.source,'snippets/nested'),'junction');fs.symlinkSync(path.join(f.source,'snippets'),path.join(dotfiles,'nested/cycle'),'junction');
 const sync=f.make();await sync.tick();assert.equal(f.get(f.browser,'snippets/linked.json'),'{"a":1}');assert.equal(f.get(f.browser,'snippets/nested/deep.json'),'{"b":1}');
 f.put(f.browser,'snippets/linked.json','{"a":2}');f.put(f.browser,'snippets/nested/deep.json','{"b":2}');await sync.tick();assert.equal(f.get(dotfiles,'linked.json'),'{"a":2}');assert.equal(f.get(dotfiles,'nested/deep.json'),'{"b":2}');assert.ok(fs.lstatSync(path.join(f.source,'snippets/linked.json')).isSymbolicLink());
 fs.unlinkSync(path.join(f.browser,'snippets/linked.json'));await sync.tick();assert.equal(fs.existsSync(path.join(f.source,'snippets/linked.json')),false);assert.equal(f.get(dotfiles,'linked.json'),'{"a":2}','deleting a linked preference removes its link, preserving the external dotfile');
});
for(const name of ['settings.json','snippets/race.json']) test(`${name}: desktop edit during asynchronous write preparation is retained`,async t=>{
 const f=fixture(t);f.put(f.source,name,'{"a":1}');const sync=f.make();await sync.tick();f.put(f.browser,name,'{"a":2}');
 const write=fs.promises.writeFile.bind(fs.promises);let injected=false;
 t.mock.method(fs.promises,'writeFile',async(file,...args)=>{await write(file,...args);if(!injected&&String(file).startsWith(path.join(f.source,name)+'.omo-')){injected=true;f.put(f.source,name,'{"a":3}');}});
 assert.equal(await sync.tick(),false);assert.equal(injected,true);assert.equal(f.get(f.source,name),'{"a":3}');await sync.tick();
 const preserved=fs.readdirSync(path.join(f.root,'conflicts')).map(file=>f.get(path.join(f.root,'conflicts'),file));assert.ok(preserved.includes('{"a":2}'));assert.ok(preserved.includes('{"a":3}'));
});
for(const name of ['settings.json','keybindings.json','tasks.json','snippets/race.json'])for(const mutation of ['write','delete'])test(`${name}: desktop save during the final native read close survives ${mutation}`,async t=>{
 const f=fixture(t),text=(a,b)=>JSON.stringify(name==='keybindings.json'?[{key:'ctrl+k',command:{a,b}}]:{a,b});
 const original=text(1,1),changed=text(2,1),saved=text(1,2),file=path.join(f.source,name);
 f.put(f.source,name,original);const sync=f.make();await sync.tick();
 if(mutation==='write')f.put(f.browser,name,changed);else fs.unlinkSync(path.join(f.browser,name));
 const open=fs.promises.open.bind(fs.promises);let reads=0,injected=false;
 t.mock.method(fs.promises,'open',async(filename,...args)=>{
  const handle=await open(filename,...args);
  if(filename===file&&++reads===3){const close=handle.close.bind(handle);handle.close=async()=>{await close();f.put(f.source,name,saved);injected=true;};}
  return handle;
 });
 await sync.tick();await sync.tick();assert.equal(injected,true);
 const values=[];for(const dir of [f.source,f.browser])if(fs.existsSync(path.join(dir,name)))values.push(f.get(dir,name));
 const backups=path.join(f.root,'conflicts');if(fs.existsSync(backups))for(const file of fs.readdirSync(backups))values.push(f.get(backups,file));
 assert.ok(values.includes(saved),'the newer desktop save must remain live or in a private conflict copy');
});
for(const name of ['settings.json','keybindings.json','tasks.json','snippets/race.json'])for(const mutation of ['write','delete'])test(`${name}: a desktop file created after displacement is never replaced by ${mutation}`,async t=>{
 const f=fixture(t),text=(a,b)=>JSON.stringify(name==='keybindings.json'?[{key:'ctrl+k',command:{a,b}}]:{a,b});
 const original=text(1,1),changed=text(2,1),saved=text(1,2);f.put(f.source,name,original);const sync=f.make();await sync.tick();
 if(mutation==='write')f.put(f.browser,name,changed);else fs.unlinkSync(path.join(f.browser,name));
 const preserve=sync.preserve.bind(sync);let injected=false;
 t.mock.method(sync,'preserve',async(file,...values)=>{await preserve(file,...values);if(!injected&&file===name){injected=true;f.put(f.source,name,saved);}});
 assert.equal(await sync.tick(),false);assert.equal(injected,true);assert.equal(f.get(f.source,name),saved);
 await sync.tick();const backups=path.join(f.root,'conflicts'),versions=fs.readdirSync(backups).map(file=>f.get(backups,file));
 assert.ok(versions.includes(saved)||f.get(f.source,name)===saved);
});
test('a failed native snapshot archive restores the original and leaves the browser edit intact',async t=>{
 const f=fixture(t),original='{"a":1}',changed='{"a":2}';f.put(f.source,'settings.json',original);const sync=f.make();await sync.tick();f.put(f.browser,'settings.json',changed);
 t.mock.method(sync,'preserve',async()=>{throw new Error('archive unavailable');});
 await assert.rejects(()=>sync.tick(),/archive unavailable/);assert.equal(f.get(f.source,'settings.json'),original);assert.equal(f.get(f.browser,'settings.json'),changed);
 assert.deepEqual(fs.readdirSync(f.source),['settings.json']);
});
test('an archive failure plus a newer desktop file retains a recovery snapshot outside the preference inventory',async t=>{
 const f=fixture(t),name='snippets/race.json',original='{"a":1}',browser='{"a":2}',desktop='{"a":3}';f.put(f.source,name,original);const sync=f.make();await sync.tick();f.put(f.browser,name,browser);
 t.mock.method(sync,'preserve',async()=>{f.put(f.source,name,desktop);throw new Error('archive unavailable');});
 let failure;try{await sync.tick();}catch(error){failure=error;}
 assert.ok(failure?.recoveryPath);assert.equal(fs.readFileSync(failure.recoveryPath,'utf8'),original);assert.equal(f.get(f.source,name),desktop);
 assert.deepEqual(await nativeFiles(f.source).list(),[name]);
});
test('native publication works on filesystems without hard links',async t=>{
 const f=fixture(t);f.put(f.source,'settings.json','{"a":1}');const sync=f.make();await sync.tick();f.put(f.browser,'settings.json','{"a":2}');
 const recovery=[];sync.onRecovery=filename=>recovery.push(filename);
 t.mock.method(fs,'linkSync',()=>{throw Object.assign(new Error('hard links unsupported'),{code:'ENOTSUP'});});
 await sync.tick();assert.equal(f.get(f.source,'settings.json'),'{"a":2}');assert.equal(f.get(f.browser,'settings.json'),'{"a":2}');
 assert.equal(recovery.length,1);assert.equal(fs.readFileSync(recovery[0],'utf8'),'{"a":1}');
 assert.deepEqual(fs.readdirSync(f.source).sort(),['settings.json',path.basename(recovery[0])].sort(),'only the intentional inode snapshot may remain');
 assert.deepEqual(await nativeFiles(f.source).list(),['settings.json']);
});
test('exclusive fallback creation cannot replace a newly saved desktop file',async t=>{
 const f=fixture(t),name='settings.json',file=path.join(f.source,name),saved='{"a":3}';f.put(f.source,name,'{"a":1}');const sync=f.make();await sync.tick();f.put(f.browser,name,'{"a":2}');
 t.mock.method(fs,'linkSync',()=>{throw Object.assign(new Error('hard links unsupported'),{code:'ENOTSUP'});});
 const open=fs.openSync.bind(fs);let injected=false;
 t.mock.method(fs,'openSync',(filename,flags,...args)=>{if(!injected&&filename===file&&flags==='wx'){injected=true;f.put(f.source,name,saved);}return open(filename,flags,...args);});
 assert.equal(await sync.tick(),false);assert.equal(injected,true);assert.equal(f.get(f.source,name),saved);
});
test('an exclusive fallback write failure never unlinks a peer replacement',async t=>{
 const f=fixture(t),name='settings.json',file=path.join(f.source,name),saved='{"a":3}';f.put(f.source,name,'{"a":1}');const sync=f.make();await sync.tick();f.put(f.browser,name,'{"a":2}');
 t.mock.method(fs,'linkSync',()=>{throw Object.assign(new Error('hard links unsupported'),{code:'ENOTSUP'});});
 const open=fs.openSync.bind(fs),write=fs.writeSync.bind(fs);let owned,injected=false;
 t.mock.method(fs,'openSync',(filename,flags,...args)=>{const fd=open(filename,flags,...args);if(filename===file&&flags==='wx')owned=fd;return fd;});
 t.mock.method(fs,'writeSync',(fd,...args)=>{if(fd===owned&&!injected){injected=true;f.put(f.source,name,saved);throw Object.assign(new Error('write failed'),{code:'EIO'});}return write(fd,...args);});
 await assert.rejects(()=>sync.tick(),/write failed/);assert.equal(injected,true);assert.equal(f.get(f.source,name),saved);
});
for(const mutation of ['write','delete'])test(`a linked desktop snippet survives a save during the final read before ${mutation}`,async t=>{
 const f=fixture(t),name='snippets/linked.json',external=path.join(f.root,'dotfile.json');fs.mkdirSync(path.join(f.source,'snippets'));fs.writeFileSync(external,'{"a":1,"b":1}');fs.symlinkSync(external,path.join(f.source,name),'file');
 const sync=f.make();await sync.tick();if(mutation==='write')f.put(f.browser,name,'{"a":2,"b":1}');else fs.unlinkSync(path.join(f.browser,name));
 const open=fs.promises.open.bind(fs.promises);let reads=0,injected=false;
 t.mock.method(fs.promises,'open',async(filename,...args)=>{const handle=await open(filename,...args);if(filename===path.join(f.source,name)&&++reads===3){const close=handle.close.bind(handle);handle.close=async()=>{await close();fs.writeFileSync(external,'{"a":1,"b":2}');injected=true;};}return handle;});
 assert.equal(await sync.tick(),false);assert.equal(injected,true);assert.equal(fs.lstatSync(path.join(f.source,name)).isSymbolicLink(),true);assert.equal(fs.readFileSync(external,'utf8'),'{"a":1,"b":2}');
});
test('an incomplete pre-existing archive cannot stand in for the displaced desktop version',async t=>{
 const f=fixture(t),name='settings.json',saved='{"a":1,"b":2}';f.put(f.source,name,'{"a":1,"b":1}');const sync=f.make();await sync.tick();
 await sync.preserve(name,Buffer.from(saved));const backups=path.join(f.root,'conflicts');fs.writeFileSync(path.join(backups,fs.readdirSync(backups)[0]),'interrupted archive');
 f.put(f.browser,name,'{"a":2,"b":1}');
 const open=fs.promises.open.bind(fs.promises);let reads=0;
 t.mock.method(fs.promises,'open',async(filename,...args)=>{const handle=await open(filename,...args);if(filename===path.join(f.source,name)&&++reads===3){const close=handle.close.bind(handle);handle.close=async()=>{await close();f.put(f.source,name,saved);};}return handle;});
 await sync.tick();await sync.tick();
 assert.ok(fs.readdirSync(backups).some(file=>f.get(backups,file)===saved),'the complete desktop version must be archived before its live file is replaced');
});
for(const name of ['settings.json','keybindings.json','tasks.json','snippets/race.json','snippets/linked.json'])for(const mutation of ['write','delete'])for(const when of ['during','after'])test(`${name}: an open desktop descriptor saved ${when} ${mutation} remains recoverable`,async t=>{
 const f=fixture(t),text=(a,b)=>JSON.stringify(name==='keybindings.json'?[{key:'ctrl+k',command:{a,b}}]:{a,b}),original=text(1,1),browser=text(2,1),saved=text(1,2);
 if(name==='snippets/linked.json'){const external=path.join(f.root,'dotfile.json');fs.writeFileSync(external,original);fs.mkdirSync(path.join(f.source,'snippets'));fs.symlinkSync(external,path.join(f.source,name),'file');}else f.put(f.source,name,original);
 const sync=f.make();await sync.tick();if(mutation==='write')f.put(f.browser,name,browser);else fs.unlinkSync(path.join(f.browser,name));
 const fd=fs.openSync(path.join(f.source,name),'r+');let injected=false;
 const save=()=>{const bytes=Buffer.from(saved);fs.ftruncateSync(fd,0);fs.writeSync(fd,bytes,0,bytes.length,0);injected=true;};
 try{
  const preserve=sync.preserve.bind(sync);t.mock.method(sync,'preserve',async(file,...values)=>{await preserve(file,...values);if(!injected&&file===name&&when==='during')save();});
  await sync.tick();if(when==='after')save();await sync.tick();assert.equal(injected,true);
 }finally{fs.closeSync(fd);}
 const copies=path.join(f.root,'conflicts');assert.ok(fs.readdirSync(copies).some(file=>f.get(copies,file)===saved),'retain the displaced inode, not only bytes copied before the descriptor save');
});
for(const name of ['settings.json','keybindings.json','tasks.json','snippets/race.json','snippets/linked.json'])for(const mutation of ['write','delete'])test(`${name}: a native inode on a different filesystem survives a descriptor save after ${mutation}`,async t=>{
 const f=fixture(t),text=(a,b)=>JSON.stringify(name==='keybindings.json'?[{key:'ctrl+k',command:{a,b}}]:{a,b}),saved=text(1,2);
 if(name==='snippets/linked.json'){const external=path.join(f.root,'dotfile.json');fs.writeFileSync(external,text(1,1));fs.mkdirSync(path.join(f.source,'snippets'));fs.symlinkSync(external,path.join(f.source,name),'file');}else f.put(f.source,name,text(1,1));
 const sync=f.make();await sync.tick();if(mutation==='write')f.put(f.browser,name,text(2,1));else fs.unlinkSync(path.join(f.browser,name));
 const link=fs.linkSync.bind(fs);t.mock.method(fs,'linkSync',(from,to)=>{if(String(to).includes('-native-')&&String(to).startsWith(path.join(f.root,'conflicts')))throw Object.assign(new Error('different filesystems'),{code:'EXDEV'});return link(from,to);});
 const recovery=[];sync.onRecovery=filename=>recovery.push(filename);const fd=fs.openSync(path.join(f.source,name),'r+');
 try{await sync.tick();const bytes=Buffer.from(saved);fs.ftruncateSync(fd,0);fs.writeSync(fd,bytes,0,bytes.length,0);assert.equal(recovery.length,1);assert.equal(fs.readFileSync(recovery[0],'utf8'),saved);}finally{fs.closeSync(fd);}
 assert.equal((await nativeFiles(f.source).list()).some(file=>file.includes('.omo-displaced-')),false);
});
