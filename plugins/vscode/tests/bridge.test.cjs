'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');
const {browserFiles}=require('../profile-bridge.cjs');
const {PreferenceSync,nativeFiles}=require('../preference-sync.cjs');
const {nativeHistory,installNativeHistory}=require('../preference-history.cjs');
for(const scheme of ['vscode-userdata','vscode-remote']) for(const name of ['settings.json','keybindings.json','tasks.json','snippets/race.json']) for(const mutation of ['write','delete']) for(const save of ['changed','unchanged','oversized']) {
 test(`${scheme}: ${name} ${save} save during provider ${mutation} remains live or preserved`,async t=>{
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-browser-race-'));
  let sync,uninstall;
  t.after(()=>{if(uninstall)uninstall();if(sync)sync.close();fs.rmSync(root,{recursive:true,force:true});});
  const source=path.join(root,'desktop'),browser=path.join(root,'browser'),backups=path.join(root,'conflicts');
  const text=value=>JSON.stringify(name==='keybindings.json'?[{key:'ctrl+k',command:value}]:value);
  const original=text({a:1,b:1}),desktop=text({a:2,b:1}),saved=save==='unchanged'?original:text({a:1,b:save==='oversized'?'x'.repeat(1024*1024+1):2});
  const put=(dir,value)=>{const file=path.join(dir,name);fs.mkdirSync(path.dirname(file),{recursive:true});fs.writeFileSync(file,value);};
  put(source,original);fs.mkdirSync(browser);
  const disk=uri=>{assert.equal(uri.scheme,scheme);return path.join(browser,...uri.path.slice('/User'.length).split('/'));};
  let armed=false,injected=false;
  const entered=Promise.withResolvers(),resume=Promise.withResolvers();
  async function boundary(uri,kind){if(armed&&uri.path==='/User/'+name&&kind===mutation){armed=false;entered.resolve();await resume.promise;}}
  const vscode={FileType:{File:1,Directory:2,SymbolicLink:64},Uri:{joinPath:(uri,...parts)=>({...uri,path:path.posix.join(uri.path,...parts)})},workspace:{fs:{
   readDirectory:async uri=>(await fs.promises.readdir(disk(uri),{withFileTypes:true})).map(e=>[e.name,e.isDirectory()?2:1]),
   readFile:async uri=>fs.promises.readFile(disk(uri)),
   writeFile:async(uri,value)=>{await boundary(uri,'write');await fs.promises.writeFile(disk(uri),value);},
   createDirectory:async uri=>fs.promises.mkdir(disk(uri),{recursive:true}),
   delete:async uri=>{await boundary(uri,'delete');await fs.promises.unlink(disk(uri));},
  }}};
  const history=path.join(root,'history');uninstall=installNativeHistory(browser,history);
  sync=new PreferenceSync({source:nativeFiles(source),target:browserFiles(vscode,{scheme,path:'/User'},nativeHistory(history)),lock:path.join(root,'lock.sqlite'),backups});
  await sync.tick();
  if(mutation==='delete')fs.unlinkSync(path.join(source,name));else put(source,desktop);
  armed=true;const pending=sync.tick();
  await entered.promise;
  try{put(browser,saved);injected=true;}finally{resume.resolve();}
  await pending;await sync.tick();
  assert.equal(injected,true);
  const versions=[];
  for(const dir of [source,browser])if(fs.existsSync(path.join(dir,name)))versions.push(fs.readFileSync(path.join(dir,name),'utf8'));
  if(fs.existsSync(backups))for(const file of fs.readdirSync(backups))versions.push(fs.readFileSync(path.join(backups,file),'utf8'));
  assert.ok(versions.includes(saved),'the newer browser save must remain live or in a conflict copy');
 });
}
test('browser adapter reads and writes the supplied user-data provider, including nested snippets and deletions',async t=>{
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-browser-provider-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 const disk=uri=>{assert.equal(uri.scheme,'vscode-userdata');assert.ok(uri.path==='/User'||uri.path.startsWith('/User/'));return path.join(root,...uri.path.slice('/User'.length).split('/'));};
 const vscode={FileType:{File:1,Directory:2,SymbolicLink:64},Uri:{joinPath:(uri,...parts)=>({...uri,path:path.posix.join(uri.path,...parts)})},workspace:{fs:{
  readDirectory:async uri=>(await fs.promises.readdir(disk(uri),{withFileTypes:true})).map(e=>[e.name,e.isDirectory()?2:1]),
  readFile:async uri=>fs.promises.readFile(disk(uri)),
  writeFile:async (uri,value)=>fs.promises.writeFile(disk(uri),value),
  createDirectory:async uri=>fs.promises.mkdir(disk(uri),{recursive:true}),
  delete:async uri=>fs.promises.unlink(disk(uri)),
 }}};
 const adapter=browserFiles(vscode,{scheme:'vscode-userdata',path:'/User'},nativeHistory(path.join(root,'.history')));
 await adapter.write('settings.json',Buffer.from('{"editor.fontSize":19}'));await adapter.write('snippets/nested/fixture.json',Buffer.from('{"fixture":{}}'));
 assert.equal((await adapter.read('settings.json')).toString(),'{"editor.fontSize":19}');assert.deepEqual((await adapter.list()).sort(),['settings.json','snippets/nested/fixture.json']);
 await adapter.write('snippets/nested/fixture.json',null);assert.equal(await adapter.read('snippets/nested/fixture.json'),null);
 await assert.rejects(()=>adapter.write('../state.vscdb',Buffer.from('must not write')),/unsupported preference/);
});
test('browser inventory follows linked preferences and bounds cyclic provider directories',async t=>{
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-browser-links-'));t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
 fs.mkdirSync(path.join(root,'snippets'));fs.mkdirSync(path.join(root,'external'));fs.writeFileSync(path.join(root,'external/a.json'),'{}');fs.symlinkSync(path.join(root,'external/a.json'),path.join(root,'snippets/a.json'),'file');fs.symlinkSync(path.join(root,'external'),path.join(root,'snippets/nested'),'junction');
 const vscode={FileType:{File:1,Directory:2,SymbolicLink:64},Uri:{joinPath:(uri,...parts)=>({...uri,path:path.join(uri.path,...parts)})},workspace:{fs:{
  readDirectory:async uri=>Promise.all((await fs.promises.readdir(uri.path,{withFileTypes:true})).map(async e=>{const stat=await fs.promises.stat(path.join(uri.path,e.name));return [e.name,e.isSymbolicLink()?64:(stat.isDirectory()?2:1)];})),
 }}};
 vscode.workspace.fs.stat=async uri=>({type:(await fs.promises.stat(uri.path)).isDirectory()?2:1});
 const adapter=browserFiles(vscode,{scheme:'vscode-remote',path:root});assert.deepEqual((await adapter.list()).sort(),['snippets/a.json','snippets/nested/a.json']);
 fs.symlinkSync(path.join(root,'snippets'),path.join(root,'external/cycle'),'junction');assert.deepEqual((await adapter.list()).sort(),['snippets/a.json','snippets/nested/a.json']);
});
