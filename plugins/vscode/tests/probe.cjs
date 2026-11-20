'use strict';
// Opt-in real publisher download + actual omo proxy/browser probe. Never a unit
// test: run only by the explicit command documented in ../README.md.
const fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const {spawn,execFileSync}=require('node:child_process');const net=require('node:net');const assert=require('node:assert/strict');
const {pinInstance,isRunning,stopChild,freshSnapshot}=require('./probe-state.cjs');
const {setup:setupProfileFixture}=require('./profile-fixture.cjs');
const {installGate,runRaces}=require('./probe-race.cjs');
const jsonc=require('../vendor/jsonc-parser/main.js');
const {chromium}=require(path.resolve(__dirname,'../../../web/node_modules/playwright'));
const editor=process.argv[2];if(!['code-server','openvscode'].includes(editor))throw new Error('Choose code-server or openvscode. Microsoft mode is unavailable.');
const sharing=process.argv.includes('--profile-sharing'),localIndex=process.argv.indexOf('--local-product'),localProduct=localIndex<0?null:process.argv[localIndex+1];
const races=process.argv.includes('--preference-races');
if(races&&!sharing)throw new Error('--preference-races requires --profile-sharing');
if(localProduct&&!['Code','VSCodium','Code - OSS'].includes(localProduct))throw new Error('Unknown local fixture product');
let sharedFixture;
let preferenceRaces;
const subdomain=process.argv.includes('--subdomain'),crossSite=process.argv.includes('--cross-site'),smoke=process.argv.includes('--smoke');
const cacheIndex=process.argv.indexOf('--cache-dir'),cache=cacheIndex<0?null:path.resolve(process.argv[cacheIndex+1]);
let cacheLease=false;
const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-editor-probe-')),home=path.join(root,'omo'),project=path.join(root,'-- workspace " 世界');
const otherProject=path.join(root,'second workspace');
const binary=path.resolve(__dirname,'../../../bin/omo'),plugin=path.join(home,'plugins','vscode');
const env={PATH:'/usr/bin:/bin',HOME:root,USERPROFILE:root,APPDATA:path.join(root,'appdata'),XDG_CONFIG_HOME:path.join(root,'config'),XDG_DATA_HOME:path.join(root,'data'),XDG_STATE_HOME:path.join(root,'state'),OMO_HOME:home,OMO_PLUGIN_CATALOG:path.join(root,'missing-catalog'),GIT_CONFIG_NOSYSTEM:'1'};
env.XDG_RUNTIME_DIR=path.join(root,'host-runtime');
// Preserve omo's explicit unsigned-agent setting without changing it.
if(process.env.GIT_CONFIG_PARAMETERS)env.GIT_CONFIG_PARAMETERS=process.env.GIT_CONFIG_PARAMETERS;
let server,browser,token='',log='',port,base;
const started=performance.now(),instances=[],children=new Map();
function trace(stage,detail={}){fs.appendFileSync(path.join(root,'lifecycle.jsonl'),JSON.stringify({ms:Math.round(performance.now()-started),stage,...detail})+'\n')}
function processes(){
 if(process.platform!=='linux')return [];
 return fs.readdirSync('/proc').filter(p=>/^\d+$/.test(p)).flatMap(pid=>{try{const raw=fs.readFileSync('/proc/'+pid+'/stat','utf8'),fields=raw.slice(raw.lastIndexOf(')')+2).split(' ');return [{pid:Number(pid),parent:Number(fields[1]),state:fields[0],start:fields[19]}]}catch{return []}});
}
function captureChildren(){
 const list=processes(),owned=new Set([server.pid]);
 for(let size=-1;size!==owned.size;){size=owned.size;for(const p of list)if(owned.has(p.parent))owned.add(p.pid)}
 for(const p of list)if(p.pid!==server.pid&&owned.has(p.pid))children.set(p.pid,p);
}
function remainingChildren(){return processes().filter(p=>children.get(p.pid)?.start===p.start&&p.state!=='Z')}
async function checkpoint(stage){const tasks=(await api('GET','/api/plugins/vscode/tasks')).tasks,forwards=(await api('GET','/api/proxy/forwards')).forwards;trace(stage,{tasks:tasks.map(({id,name,state,reason})=>({id,name,state,reason})),forwards:forwards.map(({id,task_id})=>({id,task_id})),pages:browser.contexts().map(c=>c.pages().map(p=>{const u=new URL(p.url());return {origin:u.origin,path:u.pathname}}))})}
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function wait(fn,ms=30000){const until=performance.now()+ms;for(;;){const v=await fn();if(v)return v;if(performance.now()>until)throw new Error('probe condition timed out');await pause(150)}}
async function api(method,route,body){const r=await fetch(base+route,{method,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});const text=await r.text();if(!r.ok)throw new Error(route+' '+r.status+' '+text);return text?JSON.parse(text):undefined}
async function command(page,name){await page.bringToFront();await page.keyboard.press('F1');await page.locator('.quick-input-widget input').fill('>'+name);await page.locator('.quick-input-widget .monaco-list-row').filter({hasText:name}).first().click();}
async function trustWorkspace(page){
 const config=JSON.parse(await page.locator('#vscode-workbench-web-configuration').getAttribute('data-settings'));
 assert.equal(config.enableWorkspaceTrust,false,'omo folders must open trusted without accepting a prompt');
 assert.equal(await page.getByRole('button',{name:/Yes, I trust the authors/}).count(),0);
}
async function terminalCheck(page,folder,shared=false){
 await trustWorkspace(page);await wait(()=>fs.existsSync(path.join(folder,'.omo-fixture-active')));await page.keyboard.press('Escape');await command(page,'Terminal: Create New Terminal');
 const input=page.locator('.terminal-wrapper .xterm-helper-textarea').last();await input.waitFor({state:'attached',timeout:30000});await input.focus();
 await page.keyboard.type('git config --global --get user.name > .omo-probe-terminal; printf %s "$XDG_RUNTIME_DIR" > .omo-probe-runtime; printf %s "$EDITOR_PROBE_SETTING" > .omo-probe-setting');await page.keyboard.press('Enter');
 await wait(()=>fs.existsSync(path.join(folder,'.omo-probe-terminal'))&&fs.readFileSync(path.join(folder,'.omo-probe-terminal'),'utf8').trim()==='OmoProbe',30000);
 await wait(()=>fs.existsSync(path.join(folder,'.omo-probe-runtime'))&&fs.readFileSync(path.join(folder,'.omo-probe-runtime'),'utf8')===env.XDG_RUNTIME_DIR,30000);
 if(shared)await wait(()=>fs.existsSync(path.join(folder,'.omo-probe-setting'))&&fs.readFileSync(path.join(folder,'.omo-probe-setting'),'utf8')==='retained',30000);
}
async function inspectPreferences(page,folder,expected){
 await wait(async()=>{
  const file=path.join(folder,'.omo-fixture-settings.json'),before=fs.existsSync(file)?JSON.parse(fs.readFileSync(file,'utf8')):null;
  await command(page,'omo fixture: Inspect preferences');
  await wait(()=>fs.existsSync(file)&&freshSnapshot(before,JSON.parse(fs.readFileSync(file,'utf8'))));
  const actual=JSON.parse(fs.readFileSync(file,'utf8'));return Object.entries(expected).every(([key,value])=>actual[key]===value);
 });
}
async function editCheck(page,folder){
 await page.keyboard.press('Control+P');await page.locator('.quick-input-widget input').fill('probe.txt');
 await page.locator('.quick-input-widget .monaco-list-row').filter({hasText:'probe.txt'}).first().click();
 await page.locator('.part.editor .monaco-editor .view-lines:visible').last().click({position:{x:60,y:10}});
 assert.equal(await page.evaluate(()=>!!document.activeElement?.closest('.part.editor .monaco-editor')),true,'file editor must own keyboard focus');
 await page.keyboard.press('Control+A');
 const content='Saved directly through the browser editor.\n';await page.keyboard.type(content);await page.keyboard.press('Control+S');
 await wait(()=>fs.readFileSync(path.join(folder,'probe.txt'),'utf8')===content);
}
function runtime(instance){const dir=instance.runtimePath;assert.ok(dir,'missing observed runtime path');assert.equal(fs.statSync(dir).mode&0o777,0o700);assert.ok(Buffer.byteLength(path.join(dir,'vscode-ipc-'+ 'a'.repeat(36)+'.sock'))<=103);return dir}
async function main(){
 if(cache){fs.mkdirSync(path.dirname(cache),{recursive:true});fs.mkdirSync(cache+'.in-use');cacheLease=true;}
 fs.writeFileSync(path.join(root,'.gitconfig'),'[user]\n name = OmoProbe\n');
 fs.mkdirSync(env.XDG_RUNTIME_DIR,{mode:0o700});
 const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));port=s.address().port;await new Promise(r=>s.close(r));assert.notEqual(port,8090);base='http://127.0.0.1:'+port;
 for(const folder of [project,otherProject]){
  fs.mkdirSync(folder,{recursive:true});execFileSync('git',['init','-q','-b','main'],{cwd:folder,env});
  fs.writeFileSync(path.join(folder,'probe.txt'),'Temporary editor probe only.\n');
  execFileSync('git',['add','probe.txt'],{cwd:folder,env});
  execFileSync('git',['-c','user.name=Probe','-c','user.email=probe@example.invalid','commit','-qm','test: initialize disposable editor workspace'],{cwd:folder,env});
 }
 execFileSync(binary,['config','set','port',String(port)],{env});
 if(cache){const destination=path.join(home,'plugins','.data','vscode','.artifacts-v1');fs.mkdirSync(path.dirname(destination),{recursive:true});if(fs.existsSync(cache))fs.renameSync(cache,destination);}
 if(subdomain)execFileSync(binary,['config','set','proxy_base_host','apps.localhost'],{env});
 fs.writeFileSync(path.join(home,'settings.yaml'),'# isolated probe: no agent program detection\n');
 fs.mkdirSync(plugin,{recursive:true});for(const name of ['plugin.json','editor.lua','launch.cjs','profile.cjs','profile-bridge.cjs','preference-sync.cjs','preference-history.cjs','browser-history.cjs','frontend.js'])fs.copyFileSync(path.resolve(__dirname,'..',name),path.join(plugin,name));
 fs.cpSync(path.resolve(__dirname,'../vendor'),path.join(plugin,'vendor'),{recursive:true});
 if(races)installGate(plugin,path.join(root,'race-control'));
 sharedFixture=setupProfileFixture(root,env,editor,localProduct);
 fs.writeFileSync(path.join(home,'plugins','vscode.yaml'),'source: '+JSON.stringify(plugin)+'\nenabled: true\nconfig:\n  editor: '+editor+'\n  idle_minutes: 1\n');
 server=spawn(binary,[],{env,stdio:['ignore','pipe','pipe']});server.stdout.on('data',b=>log+=b);server.stderr.on('data',b=>log+=b);
 await wait(()=>{const m=log.match(/omo: http:\/\/omo\.localhost:\d+\/#([a-f0-9]{64})/);if(!isRunning(server))throw new Error('test server exited before ready');if(m)token=m[1];return !!token});
 await api('PUT','/api/server/paused',{paused:true});
 const p=await api('POST','/api/projects',{path:project,name:'Editor probe'});
 await api('POST','/api/projects',{path:otherProject,name:'Second editor probe'});
 await wait(async()=>{const list=await api('GET','/api/plugins');const v=list.plugins.find(p=>p.name==='vscode');if(v?.state==='invalid')throw new Error('Plugin invalid: '+JSON.stringify(v));return v?.state==='ready'});
 browser=await chromium.launch({headless:true,args:['--no-proxy-server']});const context=await browser.newContext();
 // Observe before the reserved tab navigates, including bootstrap/exchange.
 // Retain paths/counts only: grants, connection queries and frame payloads stay private.
 let observedPages=0;const observed=new WeakMap();context.on('page',p=>{
  const traffic={responses:[],sockets:[]};observed.set(p,traffic);const pageID=observedPages++;trace('page-open',{pageID});p.on('close',()=>trace('page-close',{pageID}));
  p.on('response',r=>{const u=new URL(r.url());traffic.responses.push({origin:u.origin,path:u.pathname,status:r.status(),type:r.request().resourceType()})});
  p.on('websocket',ws=>{const u=new URL(ws.url()),socket={host:u.host,path:u.pathname,sent:0,received:0};traffic.sockets.push(socket);trace('socket-open',{pageID,host:u.host,path:u.pathname});ws.on('close',()=>trace('socket-close',{pageID,host:u.host,path:u.pathname}));ws.on('framesent',()=>socket.sent++);ws.on('framereceived',()=>socket.received++)});
 });
 const board=await context.newPage();
 await board.goto((subdomain&&!crossSite?'http://apps.localhost:'+port:base)+'/#'+token);await board.locator('[data-sidebar-toggle]').click();
 async function openProject(name='Editor probe') {
  await board.getByRole('tab',{name:'VS Code',exact:true}).click();
  const tab=board.locator('[data-plugin-tab="vscode:editor"]');
  await tab.getByRole('button',{name:'Open in VS Code',exact:true}).click();
  await tab.getByRole('combobox',{name:'Search projects'}).fill(name);
  await tab.getByRole('option').filter({has:board.locator('[data-pick-label]',{hasText:new RegExp('^'+name+'$')})}).click();
 }
 const popup=context.waitForEvent('page',{timeout:35*60000});await openProject();const page=await popup;
 // The same reserved tab survives the asynchronous install; no second click.
 await page.waitForURL(url=>url.pathname.includes('/proxy/')||url.hostname.endsWith('.apps.localhost'),{timeout:35*60000});
 const failed=[];page.on('response',r=>{if(r.status()>=400){const u=new URL(r.url()),item={status:r.status(),path:u.pathname};if(u.pathname.endsWith('/vscode-remote-resource')){item.resource=u.searchParams.get('path');r.text().then(body=>item.body=body.slice(0,500),()=>{});}failed.push(item)}});
 await page.locator('.monaco-workbench').waitFor({timeout:90000});
 // The server must disable the trust prompt; the probe never accepts it.
 await terminalCheck(page,project);
 await editCheck(page,project);
 await wait(async()=>{const task=await api('GET','/api/plugins/vscode/tasks');return task.tasks.some(t=>t.state==='running')});
 const first=await api('GET','/api/plugins/vscode/tasks'),forwards=await api('GET','/api/proxy/forwards');
 assert.equal(first.tasks.filter(t=>t.state==='running').length,1);assert.equal(forwards.forwards.length,1);
 const firstInstance=pinInstance(first.tasks,forwards.forwards,fs.realpathSync(project));instances.push(firstInstance);const f=forwards.forwards.find(f=>f.id===firstInstance.forwardID);assert.equal(f.host,'127.0.0.1');assert.equal(f.strip_prefix,editor==='code-server');assert.ok(!page.url().includes(token));
 const appURL=new URL(page.url()),traffic=observed.get(page);
 assert.equal(appURL.pathname,subdomain?'/':f.base_path);assert.equal(appURL.hash,'');
 assert.ok(traffic.responses.some(r=>r.path==='/_omo/proxy/bootstrap'&&r.status===200));
 assert.ok(traffic.responses.some(r=>r.path==='/_omo/proxy/exchange'&&r.status===200));
 assert.ok(traffic.responses.some(r=>r.origin===appURL.origin&&r.path===appURL.pathname&&r.type==='document'&&r.status===200));
 const assets=traffic.responses.filter(r=>r.origin===appURL.origin&&r.path.startsWith(appURL.pathname)&&['script','stylesheet'].includes(r.type)&&r.status===200);assert.ok(assets.length>0);
 await wait(()=>traffic.sockets.some(s=>s.host===appURL.host&&s.path.startsWith(appURL.pathname)&&s.sent>0&&s.received>0));
 const unauthenticated=await browser.newContext();
 try{assert.equal((await unauthenticated.request.get(appURL.origin+appURL.pathname)).status(),401)}finally{await unauthenticated.close()}
 let clientBase;
 if(editor==='openvscode'){
  clientBase=await page.locator('#vscode-workbench-web-configuration').getAttribute('data-settings').then(JSON.parse).then(c=>c.serverBasePath);
  assert.equal(clientBase,subdomain?'/':f.base_path.slice(0,-1));
 }
 const routing={publisher:true,version:editor==='code-server'?'4.145.2':'1.109.5',platform:process.platform+'-'+process.arch,mode:subdomain?'subdomain':'path',boardOutsideProxyDomain:subdomain&&crossSite,authenticatedBootstrap:true,unauthenticatedDenied:true,clientBase,assets:assets.length,websockets:traffic.sockets};
 // Second click must reuse exactly the same process and forward.
 const secondPage=context.waitForEvent('page');await openProject();const page2=await secondPage;await page2.locator('.monaco-workbench').waitFor({timeout:90000});
 const second=await api('GET','/api/plugins/vscode/tasks');assert.equal(second.tasks.filter(t=>t.state==='running').length,1);assert.deepEqual(pinInstance(second.tasks,(await api('GET','/api/proxy/forwards')).forwards,firstInstance.folder,firstInstance),firstInstance);
 const profile=path.join(home,'plugins','.data','vscode','profiles',editor),settingsFile=path.join(profile,'portable','user-data','Machine','settings.json');
 const retained=JSON.parse(fs.readFileSync(settingsFile));retained['files.autoSave']='off';retained['terminal.integrated.env.linux'].EDITOR_PROBE_SETTING='retained';fs.writeFileSync(settingsFile,JSON.stringify(retained));
 const extensionMarker=path.join(sharedFixture.extensions,'.omo-probe-preserved');fs.writeFileSync(extensionMarker,'retained across folders');
 // Keep two different folders active with the shared editor profile. Closing
 // one must reap only that stable identity while the other remains usable.
 const otherPopup=context.waitForEvent('page');await openProject('Second editor probe');const otherPage=await otherPopup;
 await otherPage.locator('.monaco-workbench').waitFor({timeout:90000});
 await terminalCheck(otherPage,otherProject,true);
 await editCheck(otherPage,otherProject);
 const both=(await api('GET','/api/plugins/vscode/tasks')).tasks.filter(t=>t.state==='running');assert.equal(both.length,2);
 assert.equal(JSON.parse(fs.readFileSync(settingsFile))['files.autoSave'],'off');assert.equal(fs.readFileSync(extensionMarker,'utf8'),'retained across folders');
 const otherInstance=pinInstance(both,(await api('GET','/api/proxy/forwards')).forwards,fs.realpathSync(otherProject));instances.push(otherInstance);
 const firstRuntime=runtime(firstInstance),otherRuntime=runtime(otherInstance);assert.notEqual(firstRuntime,otherRuntime);
 captureChildren();await checkpoint('both-active');
 assert.equal((await api('GET','/api/proxy/forwards')).forwards.length,2);
 if(sharing){
  await inspectPreferences(page,project,{theme:localProduct?(editor==='code-server'?'Light Modern':'Default Light Modern'):(editor==='code-server'?'Dark Modern':'Default Dark Modern'),chatDisabled:true,chatKnown:true});
  await command(page,'omo fixture: Set font size');
  const preferences=path.join(sharedFixture.sharedUser,'settings.json');
  await wait(()=>jsonc.parse(fs.readFileSync(preferences,'utf8'))['editor.fontSize']===22);
  fs.writeFileSync(preferences,jsonc.applyEdits(fs.readFileSync(preferences,'utf8'),jsonc.modify(fs.readFileSync(preferences,'utf8'),['editor.fontSize'],20,{formattingOptions:{insertSpaces:true,tabSize:2}})));
  await inspectPreferences(page,project,{font:20});
  await command(page,'omo fixture: Install extension');
  await wait(()=>fs.existsSync(path.join(project,'.omo-installed-active')));
  const installed=JSON.parse(fs.readFileSync(path.join(sharedFixture.extensions,'extensions.json'),'utf8')).find(e=>e.identifier.id==='omo.browser-installed');
  assert.ok(installed,'browser installation must update the shared desktop extension inventory');assert.equal(installed.location.scheme,'file');
  if(races)preferenceRaces=await runRaces({page,folder:project,sharedUser:sharedFixture.sharedUser,profile,control:path.join(root,'race-control'),command,wait});
 }
 if(smoke){
  for(const task of both)await api('POST','/api/plugins/vscode/tasks/'+encodeURIComponent(task.name)+'/stop',{});
  await wait(()=>!fs.existsSync(firstRuntime)&&!fs.existsSync(otherRuntime));await wait(()=>remainingChildren().length===0);assert.equal((await api('GET','/api/proxy/forwards')).forwards.length,0);
  if(sharing){
   await page.close();await page2.close();await otherPage.close();
   fs.unlinkSync(path.join(project,'.omo-fixture-active'));fs.unlinkSync(path.join(project,'.omo-installed-active'));
   const restartedPopup=context.waitForEvent('page');await openProject();const restarted=await restartedPopup;await restarted.locator('.monaco-workbench').waitFor({timeout:90000});
   const records=(await api('GET','/api/plugins/vscode/tasks')).tasks;
   const instance=pinInstance(records,(await api('GET','/api/proxy/forwards')).forwards,firstInstance.folder);instances.push(instance);
   assert.notEqual(instance.taskID,firstInstance.taskID);assert.notEqual(instance.forwardID,firstInstance.forwardID);if(subdomain)assert.notEqual(new URL(restarted.url()).origin,appURL.origin,'restart must recover preferences on a fresh browser origin');
   await wait(()=>fs.existsSync(path.join(project,'.omo-fixture-active'))&&fs.existsSync(path.join(project,'.omo-installed-active')));
   await inspectPreferences(restarted,project,{font:20,theme:localProduct?(editor==='code-server'?'Light Modern':'Default Light Modern'):(editor==='code-server'?'Dark Modern':'Default Dark Modern'),chatDisabled:true,chatKnown:true});
   const dir=runtime(instance);captureChildren();await api('POST','/api/plugins/vscode/tasks/'+encodeURIComponent(instance.taskName)+'/stop',{});await wait(()=>!fs.existsSync(dir));await wait(()=>remainingChildren().length===0);
  }
  const report={editor,...routing,profileSharing:sharing,localProduct,preferenceRaces,instances,processCleanup:{platform:process.platform,verified:process.platform==='linux',tracked:children.size,remaining:remainingChildren()},smoke:true,firstClick:true,reused:true,concurrentFolders:true,editedDirectly:true,terminalHostGitConfig:true,hostRuntimeRestored:true,sharedProfileRetained:true,runtimeCleanup:true,explicitStop:true,failedRequests:failed};
  fs.writeFileSync(path.join(root,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));return;
 }
 // Last-disconnect idle: active sockets must survive > 1 minute, then expire.
 console.log(editor+' workbench ready through '+(subdomain?'subdomain':'path')+' proxy; checking active/reconnect/idle');
 await pause(65000);assert.equal((await api('GET','/api/plugins/vscode/tasks')).tasks.filter(t=>t.state==='running').length,2);
 await page.close();await page2.close();await pause(5000);
 const reopened=context.waitForEvent('page');await openProject();const page3=await reopened;await page3.locator('.monaco-workbench').waitFor({timeout:90000});
 assert.deepEqual(pinInstance((await api('GET','/api/plugins/vscode/tasks')).tasks,(await api('GET','/api/proxy/forwards')).forwards,firstInstance.folder,firstInstance),firstInstance);
 await pause(65000);assert.deepEqual(pinInstance((await api('GET','/api/plugins/vscode/tasks')).tasks,(await api('GET','/api/proxy/forwards')).forwards,firstInstance.folder,firstInstance),firstInstance);
 captureChildren();const firstClosed=performance.now();await page3.close();await checkpoint('first-folder-disconnected');await wait(async()=>(await api('GET','/api/plugins/vscode/tasks')).tasks.some(t=>t.id===firstInstance.taskID&&t.state==='exited'&&t.reason==='idle'),90000);const firstIdleMs=Math.round(performance.now()-firstClosed);
 assert.deepEqual(pinInstance((await api('GET','/api/plugins/vscode/tasks')).tasks,(await api('GET','/api/proxy/forwards')).forwards,otherInstance.folder,otherInstance),otherInstance);
 await wait(()=>!fs.existsSync(firstRuntime));assert.equal(fs.existsSync(otherRuntime),true);
 assert.equal((await api('GET','/api/proxy/forwards')).forwards.length,1);await checkpoint('before-second-folder-reload');await otherPage.reload();await otherPage.locator('.monaco-workbench').waitFor({timeout:90000});
 captureChildren();const otherClosed=performance.now();await otherPage.close();await checkpoint('second-folder-reload-disconnected');await wait(async()=>(await api('GET','/api/plugins/vscode/tasks')).tasks.some(t=>t.id===otherInstance.taskID&&t.state==='exited'&&t.reason==='idle'),90000);const secondIdleMs=Math.round(performance.now()-otherClosed);assert.equal((await api('GET','/api/plugins/vscode/tasks')).tasks.filter(t=>t.state==='running').length,0);
 assert.equal((await api('GET','/api/proxy/forwards')).forwards.length,0);
 await wait(()=>!fs.existsSync(otherRuntime));await wait(()=>remainingChildren().length===0);await checkpoint('idle-cleanup-complete');assert.equal(fs.existsSync(env.XDG_RUNTIME_DIR),true);
 const report={editor,...routing,instances,disconnectToIdleMs:[firstIdleMs,secondIdleMs],processCleanup:{platform:process.platform,verified:process.platform==='linux',tracked:children.size,remaining:remainingChildren()},firstClick:true,reused:true,concurrentFolders:true,editedDirectly:true,terminalHostGitConfig:true,hostRuntimeRestored:true,sharedProfileRetained:true,separateTmp:true,runtimeCleanup:true,independentCleanup:true,activeBeyondIdle:true,reconnect:true,idleCleanup:true,failedRequests:failed};
 fs.writeFileSync(path.join(root,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));
}
main().catch(async error=>{
 if(token){const snapshot={tasks:await api('GET','/api/plugins/vscode/tasks').catch(()=>undefined),forwards:await api('GET','/api/proxy/forwards').catch(()=>undefined)};fs.writeFileSync(path.join(root,'failure-state.json'),JSON.stringify(snapshot,null,2));}
 if(browser){const pages=browser.contexts().flatMap(c=>c.pages());for(let i=0;i<pages.length;i++){const u=new URL(pages[i].url());console.error('Probe page '+i+': '+u.origin+u.pathname);}for(let i=0;i<pages.length;i++)await pages[i].screenshot({path:path.join(root,'failure-'+i+'.png')}).catch(()=>{});}
 console.error(String(error.stack||error).replaceAll(token||'no-token','[test-token]'));process.exitCode=1;
}).finally(async()=>{if(server?.pid&&isRunning(server))captureChildren();await browser?.close();await stopChild(server);
 await wait(()=>remainingChildren().length===0).catch(error=>{console.error(error.message);process.exitCode=1});fs.writeFileSync(path.join(root,'shutdown.json'),JSON.stringify({serverExit:server?.exitCode,serverSignal:server?.signalCode,browserConnected:browser?.isConnected()??false,trackedChildren:[...children.values()],remainingChildren:remainingChildren()},null,2));
 if(cacheLease){const source=path.join(home,'plugins','.data','vscode','.artifacts-v1');if(fs.existsSync(source))fs.renameSync(source,cache);fs.rmdirSync(cache+'.in-use');}
 console.log('Isolated probe files retained at '+root);});
