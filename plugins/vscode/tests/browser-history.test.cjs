'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {installBrowserHistory}=require('../browser-history.cjs');
const fs=require('node:fs'),os=require('node:os'),path=require('node:path');
const {PreferenceSync,nativeFiles}=require('../preference-sync.cjs');
const {browserFiles}=require('../profile-bridge.cjs');

// IDB requests are queued in order and may enqueue further requests from their
// success handlers. Commit applies the entire queue or rolls the transaction
// back. This fixture has no event-loop or elapsed-time assumptions.
function database(t){
 const committed=new Map();
 class Store{
  constructor(){this.name='vscode-userdata-store';this.transaction={db:{name:'vscode-web-db'},abort:()=>{this.aborted=true;}};this.data=new Map(committed);this.requests=[];}
  request(operation){const request={};this.requests.push(()=>{request.result=operation();request.onsuccess?.();});return request;}
  get(key){return this.request(()=>this.data.get(key));}
  put(value,key){return this.request(()=>{this.data.set(key,structuredClone(value));return key;});}
  delete(key){return this.request(()=>this.data.delete(key));}
  commit(){while(this.requests.length&&!this.aborted)this.requests.shift()();if(this.aborted)throw new Error('transaction aborted');committed.clear();for(const pair of this.data)committed.set(...pair);}
 }
 const restore=installBrowserHistory({IDBObjectStore:Store,btoa:text=>Buffer.from(text,'binary').toString('base64')});t.after(restore);
 const encode=value=>ArrayBuffer.isView(value)?Buffer.from(value):Buffer.from(typeof value==='string'?value:JSON.stringify(value));
 const write=(key,value)=>{const store=new Store();store.put(encode(value),key);store.commit();};
 const history=()=>JSON.parse(Buffer.from(committed.get('/User/.omo-preference-history.json')).toString());
 const vscode={FileType:{File:1,Directory:2,SymbolicLink:64},Uri:{joinPath:(uri,...parts)=>({...uri,path:path.posix.join(uri.path,...parts)})},workspace:{fs:{
  readDirectory:async uri=>{
   const children=new Map();for(const key of committed.keys())if(key.startsWith(uri.path+'/')){const parts=key.slice(uri.path.length+1).split('/');children.set(parts[0],parts.length>1?2:1);}return [...children];
  },
  readFile:async uri=>{if(!committed.has(uri.path))throw Object.assign(new Error('missing'),{code:'FileNotFound'});return committed.get(uri.path);},
  writeFile:async(uri,value)=>write(uri.path,value),
  createDirectory:async()=>{},
  delete:async uri=>{const store=new Store();store.delete(uri.path);store.commit();},
 }}};
 return {Store,committed,write,history,encode,vscode};
}
for(const name of ['settings.json','keybindings.json','tasks.json','snippets/race.json'])for(const mutation of ['write','delete']){
 test(`${name}: IndexedDB ${mutation} preserves a save committed during an in-flight bridge mutation`,t=>{
  const db=database(t),key='/User/'+name,saved=name==='keybindings.json'?'[{"key":"ctrl+k","command":"saved"}]':'{"a":1,"b":2}';
  db.write(key,name==='keybindings.json'?'[]':'{"a":1,"b":1}');
  // A user save commits while the extension API operation is pending. Its
  // journal write shares its transaction, before the older operation resumes.
  db.write(key,saved);
  if(mutation==='write')db.write(key,name==='keybindings.json'?'[{"key":"ctrl+k","command":"desktop"}]':'{"a":2,"b":1}');
  else{const store=new db.Store();store.delete(key);store.commit();}
  assert.ok(db.history().entries.some(entry=>entry.name===name&&Buffer.from(entry.value,'base64').toString()===saved));
 });
}
test('a concurrent acknowledgement cannot discard newer records, including several operations in one transaction',t=>{
 const db=database(t),key='/User/settings.json';db.write(key,'{"a":1}');const through=db.history().next-1;
 const store=new db.Store();store.put(db.encode('{"a":2}'),key);store.put(db.encode({through,nonce:'ready'}),'/User/.omo-preference-ack.json');store.put(db.encode('{"a":3}'),key);store.commit();
 assert.equal(db.history().ready,'ready');assert.deepEqual(db.history().entries.map(entry=>Buffer.from(entry.value,'base64').toString()),['{"a":2}','{"a":3}']);
});
test('an aborted IndexedDB transaction commits neither a preference mutation nor its journal',t=>{
 const db=database(t),key='/User/settings.json';db.write(key,'{"a":1}');const before=db.history();
 const store=new db.Store();store.put(db.encode('{"a":2}'),key);store.transaction.abort();assert.throws(()=>store.commit(),/aborted/);
 assert.equal(Buffer.from(db.committed.get(key)).toString(),'{"a":1}');assert.deepEqual(db.history(),before);
});
test('a full browser journal aborts the preference write and can resume after acknowledgement',t=>{
 const db=database(t),key='/User/settings.json';db.write(key,'{"a":1}');
 const entries=Array.from({length:4096},(_,index)=>({id:index+1,name:'settings.json',value:Buffer.from('{}').toString('base64')}));
 db.committed.set('/User/.omo-preference-history.json',db.encode({version:1,next:4097,entries}));
 assert.throws(()=>db.write(key,'{"a":2}'),/transaction aborted/);assert.equal(Buffer.from(db.committed.get(key)).toString(),'{"a":1}');
 db.write('/User/.omo-preference-ack.json',{through:4096});db.write(key,'{"a":2}');assert.equal(Buffer.from(db.committed.get(key)).toString(),'{"a":2}');
});
test('native databases, secrets, other profiles and other IndexedDB stores are not journaled',t=>{
 const db=database(t);
 for(const key of ['/User/globalStorage/state.vscdb','/User/workspaceStorage/secrets','/User/profiles/another/settings.json','/User/snippets/../state.vscdb'])db.write(key,'secret');
 const store=new db.Store();store.name='other-store';store.put(db.encode('secret'),'/User/settings.json');store.commit();
 assert.equal(db.committed.has('/User/.omo-preference-history.json'),false);
});
for(const name of ['settings.json','keybindings.json','tasks.json','snippets/race.json'])for(const mutation of ['write','delete'])for(const save of ['changed','unchanged','oversized']){
 test(`${name}: bridge archives the ${save} IndexedDB save during provider ${mutation} before acknowledging it`,async t=>{
  const db=database(t),root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-idb-history-')),desktop=path.join(root,'desktop'),backups=path.join(root,'conflicts');let sync;
  t.after(()=>{if(sync)sync.close();fs.rmSync(root,{recursive:true,force:true});});
  const text=(a,b)=>JSON.stringify(name==='keybindings.json'?[{key:'ctrl+k',command:{a,b}}]:{a,b});
  const original=text(1,1),desired=text(2,1),saved=save==='unchanged'?original:text(1,save==='oversized'?'x'.repeat(1024*1024+1):2);
  const file=path.join(desktop,name);fs.mkdirSync(path.dirname(file),{recursive:true});fs.writeFileSync(file,original);
  const api=db.vscode.workspace.fs,operation=mutation==='write'?'writeFile':'delete',originalOperation=api[operation];let armed=false;
  const entered=Promise.withResolvers(),release=Promise.withResolvers();
  api[operation]=async(uri,...args)=>{if(armed&&uri.path==='/User/'+name){armed=false;entered.resolve();await release.promise;}return originalOperation(uri,...args);};
  sync=new PreferenceSync({source:nativeFiles(desktop),target:browserFiles(db.vscode,{scheme:'vscode-userdata',path:'/User'}),lock:path.join(root,'lock.sqlite'),backups});
  await sync.tick();if(mutation==='write')fs.writeFileSync(file,desired);else fs.unlinkSync(file);
  armed=true;const pending=sync.tick();await entered.promise;
  try{db.write('/User/'+name,saved);}finally{release.resolve();}
  await pending;await sync.tick();
  assert.ok(fs.readdirSync(backups).some(entry=>fs.readFileSync(path.join(backups,entry),'utf8')===saved));
  assert.deepEqual(db.history().entries,[],'only acknowledge after the private conflict copy is durable');
 });
}
test('a stale journal without the publisher hook cannot permit unsafe mutations',async t=>{
 const db=database(t);db.committed.set('/User/.omo-preference-history.json',db.encode({version:1,next:1,entries:[],ready:'stale'}));
 db.vscode.workspace.fs.writeFile=async(uri,value)=>db.committed.set(uri.path,value);
 await assert.rejects(()=>browserFiles(db.vscode,{scheme:'vscode-userdata',path:'/User'}).write('settings.json',Buffer.from('{}'),null),/history is unavailable/);
 assert.equal(db.committed.has('/User/settings.json'),false);
});
