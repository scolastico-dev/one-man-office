'use strict';
// Opt-in publisher probe only. The installed disposable bridge is gated after
// its final expected-content check, matching the reviewer's provider fixture.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {randomUUID}=require('node:crypto');
const jsonc=require('../vendor/jsonc-parser/main.js');
function installGate(plugin,control){
 fs.mkdirSync(control,{recursive:true});
 const file=path.join(plugin,'profile-bridge.cjs');let source=fs.readFileSync(file,'utf8');
 const write='await api.writeFile(uri, value);',remove='try { await api.delete(uri, {recursive: false, useTrash: false}); }';
 assert.equal(source.split(write).length,2);assert.equal(source.split(remove).length,2);
 source=source.replace(write,"await __omoProbeGate(name, 'write');\n      "+write).replace(remove,"await __omoProbeGate(name, 'delete');\n      "+remove);
 source+=`\nasync function __omoProbeGate(name,kind){
  const control=${JSON.stringify(control)},armed=path.join(control,'armed.json');let request;
  try{request=JSON.parse(fs.readFileSync(armed,'utf8'));}catch(error){if(error.code==='ENOENT')return;throw error;}
  if(request.name!==name||request.kind!==kind)return;
  try{fs.renameSync(armed,path.join(control,request.id+'.entered'));}catch(error){if(error.code==='ENOENT')return;throw error;}
  const release=path.join(control,request.id+'.release');
  await new Promise((resolve,reject)=>{const watcher=fs.watch(control,check);watcher.on('error',error=>{watcher.close();reject(error)});function check(){if(fs.existsSync(release)){watcher.close();resolve();}}check();});
 }\n`;
 fs.writeFileSync(file,source);
}
async function runRaces({page,folder,sharedUser,profile,control,command,wait}){
 async function browser(request){
  const id=randomUUID(),response=path.join(folder,'.omo-preference-response.json');
  fs.writeFileSync(path.join(folder,'.omo-preference-request.json'),JSON.stringify({...request,id}));
  await command(page,'omo fixture: Preference file');
  const result=await wait(()=>{if(!fs.existsSync(response))return;const value=JSON.parse(fs.readFileSync(response,'utf8'));return value.id===id?value:false;});
  assert.equal(result.error,undefined);return result.text;
 }
 const cases=[];
 for(const name of ['settings.json','keybindings.json','tasks.json','snippets/race.json']){
  const file=path.join(sharedUser,...name.split('/')),retained=fs.existsSync(file)?fs.readFileSync(file,'utf8'):null;
  for(const kind of ['write','delete']){
   const id=randomUUID();
   const text=(a,b)=>name==='keybindings.json'?JSON.stringify([{key:'ctrl+k',command:'fixture.'+id+a+b}]):JSON.stringify({...((name==='settings.json'&&retained)?jsonc.parse(retained):{}),'omo.probe.a':a,'omo.probe.b':b,'omo.probe.id':id});
   const original=text(1,1),desktop=text(2,1),saved=text(1,2);
   fs.mkdirSync(path.dirname(file),{recursive:true});fs.writeFileSync(file,original);
   await wait(async()=>await browser({name,action:'read'})===original);
   fs.writeFileSync(path.join(control,'armed.json'),JSON.stringify({name,kind,id}));
   if(kind==='write')fs.writeFileSync(file,desktop);else fs.unlinkSync(file);
   await wait(()=>fs.existsSync(path.join(control,id+'.entered')));
   try{await browser({name,action:'write',text:saved});}finally{fs.writeFileSync(path.join(control,id+'.release'),'release');}
   const conflicts=path.join(profile,'preference-conflicts');
   await wait(()=>fs.existsSync(conflicts)&&fs.readdirSync(conflicts).some(entry=>fs.readFileSync(path.join(conflicts,entry),'utf8')===saved));
   cases.push({name,kind,preserved:true});
  }
  if(retained===null)fs.rmSync(file,{force:true});else fs.writeFileSync(file,retained);
  await wait(async()=>await browser({name,action:'read'})===retained);
 }
 return cases;
}
module.exports={installGate,runRaces};
