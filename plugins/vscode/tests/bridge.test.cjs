'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');
const {browserFiles}=require('../profile-bridge.cjs');
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
 const adapter=browserFiles(vscode,{scheme:'vscode-userdata',path:'/User'});
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
