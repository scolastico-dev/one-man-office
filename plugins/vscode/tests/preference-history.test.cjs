'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),os=require('node:os'),path=require('node:path');
const {promisify}=require('node:util');
const {nativeHistory,installNativeHistory}=require('../preference-history.cjs');
function fixture(t){
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-history-')),user=path.join(root,'User'),directory=path.join(root,'history');fs.mkdirSync(user);
 const uninstall=installNativeHistory(user,directory);t.after(()=>{uninstall();fs.rmSync(root,{recursive:true,force:true});});
 return {root,user,directory,history:nativeHistory(directory)};
}
for(const method of ['sync','promise','callback','descriptor','atomic'])test(`native ${method} saves retain complete preference payloads before acknowledgement`,async t=>{
 const f=fixture(t),filename=path.join(f.user,'settings.json'),value=Buffer.from('\uFEFF// saved\r\n{"a":1,"b":2,}\r\n');
 if(method==='sync')fs.writeFileSync(filename,value);
 if(method==='promise')await fs.promises.writeFile(filename,value);
 if(method==='callback')await promisify(fs.writeFile)(filename,value);
 if(method==='descriptor'||method==='atomic'){
  const target=method==='atomic'?filename+'.vsctmp':filename;
  const fd=await promisify(fs.open)(target,'w');
  try{const first=await promisify(fs.write)(fd,value,0,7,0);assert.equal(first.bytesWritten,7);assert.equal(first.buffer,value);const second=await promisify(fs.write)(fd,value,7,value.length-7,7);assert.equal(second.bytesWritten,value.length-7);}finally{await promisify(fs.close)(fd);}
  if(method==='atomic')fs.renameSync(target,filename);
 }
 const entries=await f.history.entries();assert.ok(entries.some(entry=>entry.name==='settings.json'&&entry.value.equals(value)));
 assert.equal(fs.readFileSync(filename).equals(value),true);
 await f.history.acknowledge(entries);assert.deepEqual(await f.history.entries(),[]);
});
test('acknowledgement removes only the immutable versions it read',async t=>{
 const f=fixture(t),filename=path.join(f.user,'settings.json');fs.writeFileSync(filename,'{"a":1}');const entries=await f.history.entries();fs.writeFileSync(filename,'{"a":2}');await f.history.acknowledge(entries);
 assert.deepEqual((await f.history.entries()).map(entry=>entry.value.toString()),['{"a":2}']);
});
test('only allowed preferences are recorded, with private permissions and no native databases',async t=>{
 const f=fixture(t);
 for(const name of ['globalStorage/state.vscdb','workspaceStorage/secret','profiles/another/settings.json']){const filename=path.join(f.user,name);fs.mkdirSync(path.dirname(filename),{recursive:true});fs.writeFileSync(filename,'secret');}
 assert.deepEqual(await f.history.entries(),[]);
 fs.mkdirSync(path.join(f.user,'snippets'));fs.writeFileSync(path.join(f.user,'snippets/nested.json'),'{}');const [entry]=await f.history.entries();assert.equal(entry.name,'snippets/nested.json');
 // Windows has no Unix permission bits.
 if(process.platform!=='win32')assert.equal(fs.statSync(path.join(f.directory,entry.id)).mode&0o777,0o600);
});
