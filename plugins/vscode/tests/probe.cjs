'use strict';
// Opt-in real publisher download + actual omo proxy/browser probe. Never a unit
// test: run only by the explicit command documented in ../README.md.
const fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const {spawn,execFileSync}=require('node:child_process');const net=require('node:net');const assert=require('node:assert/strict');
const {chromium}=require(path.resolve(__dirname,'../../../web/node_modules/playwright'));
const editor=process.argv[2];if(!['code-server','openvscode'].includes(editor))throw new Error('Choose code-server or openvscode. Microsoft mode is unavailable.');
const subdomain=process.argv.includes('--subdomain'),crossSite=process.argv.includes('--cross-site'),smoke=process.argv.includes('--smoke');
const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-editor-probe-')),home=path.join(root,'omo'),project=path.join(root,'-- workspace " 世界');
const otherProject=path.join(root,'second workspace');
const binary=path.resolve(__dirname,'../../../bin/omo'),plugin=path.join(home,'plugins','vscode');
const env={PATH:'/usr/bin:/bin',HOME:root,USERPROFILE:root,APPDATA:path.join(root,'appdata'),XDG_CONFIG_HOME:path.join(root,'config'),XDG_DATA_HOME:path.join(root,'data'),XDG_STATE_HOME:path.join(root,'state'),OMO_HOME:home,OMO_PLUGIN_CATALOG:path.join(root,'missing-catalog'),GIT_CONFIG_NOSYSTEM:'1'};
env.XDG_RUNTIME_DIR=path.join(root,'host-runtime');
// Preserve omo's explicit unsigned-agent setting without changing it.
if(process.env.GIT_CONFIG_PARAMETERS)env.GIT_CONFIG_PARAMETERS=process.env.GIT_CONFIG_PARAMETERS;
let server,browser,token='',log='',port,base;
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function wait(fn,ms=30000){const until=Date.now()+ms;for(;;){const v=await fn();if(v)return v;if(Date.now()>until)throw new Error('probe condition timed out');await pause(150)}}
async function api(method,route,body){const r=await fetch(base+route,{method,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});const text=await r.text();if(!r.ok)throw new Error(route+' '+r.status+' '+text);return text?JSON.parse(text):undefined}
async function command(page,name){await page.keyboard.press('F1');await page.locator('.quick-input-widget input').fill('>'+name);await page.locator('.quick-input-widget .monaco-list-row').filter({hasText:name}).first().click();}
async function trustWorkspace(page){
 const initial=page.getByRole('button',{name:/Yes, I trust the authors/});
 if(await initial.waitFor({state:'visible',timeout:15000}).then(()=>true,()=>false)){await initial.click();return}
 await command(page,'Workspaces: Manage Workspace Trust');
 await wait(async()=>{if(await page.getByText('You trust this folder',{exact:true}).isVisible())return true;const trust=page.getByRole('button',{name:/^Trust/}).first();if(await trust.count()){await trust.click();return true}return false});
 const confirm=page.getByRole('button',{name:/Yes, I trust the authors/});if(await confirm.count())await confirm.click();
}
async function terminalCheck(page,folder,shared=false){
 await trustWorkspace(page);await page.keyboard.press('Escape');await command(page,'Terminal: Create New Terminal');
 const input=page.locator('.terminal-wrapper .xterm-helper-textarea').last();await input.waitFor({state:'attached',timeout:30000});await input.focus();
 await page.keyboard.type('git config --global --get user.name > .omo-probe-terminal; printf %s "$XDG_RUNTIME_DIR" > .omo-probe-runtime; printf %s "$EDITOR_PROBE_SETTING" > .omo-probe-setting');await page.keyboard.press('Enter');
 await wait(()=>fs.existsSync(path.join(folder,'.omo-probe-terminal'))&&fs.readFileSync(path.join(folder,'.omo-probe-terminal'),'utf8').trim()==='OmoProbe',30000);
 await wait(()=>fs.existsSync(path.join(folder,'.omo-probe-runtime'))&&fs.readFileSync(path.join(folder,'.omo-probe-runtime'),'utf8')===env.XDG_RUNTIME_DIR,30000);
 if(shared)await wait(()=>fs.existsSync(path.join(folder,'.omo-probe-setting'))&&fs.readFileSync(path.join(folder,'.omo-probe-setting'),'utf8')==='retained',30000);
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
function runtime(task){const line=task.output.find(s=>s.startsWith('Runtime: '));assert.ok(line,'missing runtime path');const dir=JSON.parse(line.slice(9));assert.equal(fs.statSync(dir).mode&0o777,0o700);assert.ok(Buffer.byteLength(path.join(dir,'vscode-ipc-'+ 'a'.repeat(36)+'.sock'))<=103);return dir}
async function main(){
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
 if(subdomain)execFileSync(binary,['config','set','proxy_base_host','apps.localhost'],{env});
 fs.writeFileSync(path.join(home,'settings.yaml'),'# isolated probe: no agent program detection\n');
 fs.mkdirSync(plugin,{recursive:true});for(const name of ['plugin.json','editor.lua','launch.cjs','frontend.js'])fs.copyFileSync(path.resolve(__dirname,'..',name),path.join(plugin,name));
 fs.writeFileSync(path.join(home,'plugins','vscode.yaml'),'source: '+JSON.stringify(plugin)+'\nenabled: true\nconfig:\n  editor: '+editor+'\n  idle_minutes: 1\n');
 server=spawn(binary,[],{env,stdio:['ignore','pipe','pipe']});server.stdout.on('data',b=>log+=b);server.stderr.on('data',b=>log+=b);
 await wait(()=>{const m=log.match(/omo: http:\/\/omo\.localhost:\d+\/#([a-f0-9]{64})/);if(server.exitCode!==null)throw new Error('test server exited before ready');if(m)token=m[1];return !!token});
 await api('PUT','/api/server/paused',{paused:true});
 const p=await api('POST','/api/projects',{path:project,name:'Editor probe'});
 await api('POST','/api/projects',{path:otherProject,name:'Second editor probe'});
 await wait(async()=>{const list=await api('GET','/api/plugins');const v=list.plugins.find(p=>p.name==='vscode');if(v?.state==='invalid')throw new Error('Plugin invalid: '+JSON.stringify(v));return v?.state==='ready'});
 browser=await chromium.launch({headless:true,args:['--no-proxy-server']});const context=await browser.newContext();const board=await context.newPage();
 await board.goto((subdomain&&!crossSite?'http://apps.localhost:'+port:base)+'/#'+token);await board.locator('[data-sidebar-toggle]').click();
 const button=board.locator('.sidebar-plugins > section[aria-label="Editor probe"] button').filter({hasText:'Open in VS Code'});await button.waitFor();
 const popup=context.waitForEvent('page',{timeout:35*60000});await button.click();const page=await popup;
 // The same reserved tab survives the asynchronous install; no second click.
 await page.waitForURL(url=>url.pathname.includes('/proxy/')||url.hostname.endsWith('.apps.localhost'),{timeout:35*60000});
 const failed=[];page.on('response',r=>{if(r.status()>=400){const u=new URL(r.url()),item={status:r.status(),path:u.pathname};if(u.pathname.endsWith('/vscode-remote-resource')){item.resource=u.searchParams.get('path');r.text().then(body=>item.body=body.slice(0,500),()=>{});}failed.push(item)}});
 await page.locator('.monaco-workbench').waitFor({timeout:90000});
 // Accept the normal workspace trust prompt in this disposable project only.
 await terminalCheck(page,project);
 await editCheck(page,project);
 await wait(async()=>{const task=await api('GET','/api/plugins/vscode/tasks');return task.tasks.some(t=>t.state==='running')});
 const first=await api('GET','/api/plugins/vscode/tasks'),forwards=await api('GET','/api/proxy/forwards');
 assert.equal(first.tasks.filter(t=>t.state==='running').length,1);assert.equal(forwards.forwards.length,1);
 const f=forwards.forwards[0];assert.equal(f.host,'127.0.0.1');assert.equal(f.strip_prefix,editor==='code-server');assert.ok(!page.url().includes(token));
 // Second click must reuse exactly the same process and forward.
 const secondPage=context.waitForEvent('page');await button.click();const page2=await secondPage;await page2.locator('.monaco-workbench').waitFor({timeout:90000});
 const second=await api('GET','/api/plugins/vscode/tasks');assert.equal(second.tasks.filter(t=>t.state==='running').length,1);assert.equal(second.tasks.find(t=>t.state==='running').id,first.tasks.find(t=>t.state==='running').id);
 const profile=path.join(home,'plugins','.data','vscode','profiles',editor),settingsFile=path.join(profile,'portable','user-data','Machine','settings.json');
 const retained=JSON.parse(fs.readFileSync(settingsFile));retained['files.autoSave']='off';retained['terminal.integrated.env.linux'].EDITOR_PROBE_SETTING='retained';fs.writeFileSync(settingsFile,JSON.stringify(retained));
 const extensionMarker=path.join(profile,'extensions','.omo-probe-preserved');fs.writeFileSync(extensionMarker,'retained across folders');
 // Keep two different folders active with the shared editor profile. Closing
 // one must reap only that stable identity while the other remains usable.
 const otherButton=board.locator('.sidebar-plugins > section[aria-label="Second editor probe"] button').filter({hasText:'Open in VS Code'});
 const otherPopup=context.waitForEvent('page');await otherButton.click();const otherPage=await otherPopup;
 await otherPage.locator('.monaco-workbench').waitFor({timeout:90000});
 await terminalCheck(otherPage,otherProject,true);
 await editCheck(otherPage,otherProject);
 const both=(await api('GET','/api/plugins/vscode/tasks')).tasks.filter(t=>t.state==='running');assert.equal(both.length,2);
 assert.equal(JSON.parse(fs.readFileSync(settingsFile))['files.autoSave'],'off');assert.equal(fs.readFileSync(extensionMarker,'utf8'),'retained across folders');
 const firstRuntime=runtime(both.find(t=>t.id===first.tasks.find(t=>t.state==='running').id)),otherRuntime=runtime(both.find(t=>t.id!==first.tasks.find(t=>t.state==='running').id));assert.notEqual(firstRuntime,otherRuntime);
 const otherID=both.find(t=>t.id!==first.tasks.find(t=>t.state==='running').id).id;
 assert.equal((await api('GET','/api/proxy/forwards')).forwards.length,2);
 if(smoke){
  for(const task of both)await api('POST','/api/plugins/vscode/tasks/'+encodeURIComponent(task.name)+'/stop',{});
  await wait(()=>!fs.existsSync(firstRuntime)&&!fs.existsSync(otherRuntime));assert.equal((await api('GET','/api/proxy/forwards')).forwards.length,0);
  const report={editor,smoke:true,firstClick:true,reused:true,concurrentFolders:true,editedDirectly:true,terminalHostGitConfig:true,hostRuntimeRestored:true,sharedProfileRetained:true,runtimeCleanup:true,explicitStop:true,failedRequests:failed};
  fs.writeFileSync(path.join(root,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));return;
 }
 // Last-disconnect idle: active sockets must survive > 1 minute, then expire.
 console.log(editor+' workbench ready through '+(subdomain?'subdomain':'path')+' proxy; checking active/reconnect/idle');
 await pause(65000);assert.equal((await api('GET','/api/plugins/vscode/tasks')).tasks.filter(t=>t.state==='running').length,2);
 await page.close();await page2.close();await pause(5000);
 const reopened=context.waitForEvent('page');await button.click();const page3=await reopened;await page3.locator('.monaco-workbench').waitFor({timeout:90000});
 assert.equal((await api('GET','/api/plugins/vscode/tasks')).tasks.find(t=>t.state==='running').id,first.tasks.find(t=>t.state==='running').id);
 await pause(65000);assert.ok((await api('GET','/api/plugins/vscode/tasks')).tasks.some(t=>t.state==='running'));
 await page3.close();await wait(async()=>!(await api('GET','/api/plugins/vscode/tasks')).tasks.some(t=>t.state==='running'&&t.id!==otherID),90000);
 assert.equal((await api('GET','/api/plugins/vscode/tasks')).tasks.find(t=>t.state==='running').id,otherID);
 await wait(()=>!fs.existsSync(firstRuntime));assert.equal(fs.existsSync(otherRuntime),true);
 assert.equal((await api('GET','/api/proxy/forwards')).forwards.length,1);await otherPage.reload();await otherPage.locator('.monaco-workbench').waitFor({timeout:90000});
 await otherPage.close();await wait(async()=>!(await api('GET','/api/plugins/vscode/tasks')).tasks.some(t=>t.state==='running'),90000);
 assert.equal((await api('GET','/api/proxy/forwards')).forwards.length,0);
 await wait(()=>!fs.existsSync(otherRuntime));assert.equal(fs.existsSync(env.XDG_RUNTIME_DIR),true);
 const report={editor,version:editor==='code-server'?'4.145.2':'1.109.5',platform:process.platform+'-'+process.arch,mode:subdomain?'subdomain':'path',boardOutsideProxyDomain:subdomain&&crossSite,firstClick:true,reused:true,concurrentFolders:true,editedDirectly:true,terminalHostGitConfig:true,hostRuntimeRestored:true,sharedProfileRetained:true,separateTmp:true,runtimeCleanup:true,independentCleanup:true,activeBeyondIdle:true,reconnect:true,idleCleanup:true,failedRequests:failed};
 fs.writeFileSync(path.join(root,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));
}
main().catch(async error=>{
 if(token){const snapshot={tasks:await api('GET','/api/plugins/vscode/tasks').catch(()=>undefined),forwards:await api('GET','/api/proxy/forwards').catch(()=>undefined)};fs.writeFileSync(path.join(root,'failure-state.json'),JSON.stringify(snapshot,null,2));}
 if(browser){const pages=browser.contexts().flatMap(c=>c.pages());for(let i=0;i<pages.length;i++){const u=new URL(pages[i].url());console.error('Probe page '+i+': '+u.origin+u.pathname);}for(let i=0;i<pages.length;i++)await pages[i].screenshot({path:path.join(root,'failure-'+i+'.png')}).catch(()=>{});}
 console.error(String(error.stack||error).replaceAll(token||'no-token','[test-token]'));process.exitCode=1;
}).finally(async()=>{await browser?.close();if(server&&server.exitCode===null){server.kill('SIGTERM');await Promise.race([new Promise(r=>server.on('close',r)),pause(15000).then(()=>server.kill('SIGKILL'))]);}
 console.log('Isolated probe files retained at '+root);});
