const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const net = require('node:net');
const { spawn } = require('node:child_process');
const launch = path.resolve(__dirname, '../launch.cjs');
function setup(t, script, editor='code-server') {
  const root=fs.mkdtempSync(path.join(os.tmpdir(),'omo-editor-test-'));
  t.after(()=>fs.rmSync(root,{recursive:true,force:true}));
  const folder=path.join(root,'-- literal " 世界 $()');fs.mkdirSync(folder);
  const cache=path.join(root,'verified cache'); const entry=path.join(cache,editor==='code-server'?'out/node/entry.js':'out/server-main.js');
  fs.mkdirSync(path.dirname(entry),{recursive:true});fs.writeFileSync(entry,script);
  // Model the publisher's util.paths defaults; the actual editor fixture below
  // writes through them, catching wrapper logs/heartbeat outside CLI data flags.
  if(editor==='code-server')fs.writeFileSync(path.join(path.dirname(entry),'util.js'),`const p=require('node:path');exports.paths={data:p.join(process.env.XDG_DATA_HOME,'code-server'),config:p.join(process.env.XDG_CONFIG_HOME,'code-server'),runtime:p.join(process.env.XDG_RUNTIME_DIR,'code-server')};`);
  const host={HOME:'host-home',USERPROFILE:'host-profile',XDG_CONFIG_HOME:'host-config',XDG_DATA_HOME:'host-data',XDG_CACHE_HOME:'host-cache',XDG_STATE_HOME:'host-state',XDG_RUNTIME_DIR:'host-runtime',APPDATA:'host-roaming',LOCALAPPDATA:'host-local'};
  for(const key of Object.keys(host)){host[key]=path.join(root,host[key]);fs.mkdirSync(host[key]);}
  fs.writeFileSync(path.join(host.HOME,'.gitconfig'),'[omo]\n launcherFixture = host-config-readable\n');
  fs.mkdirSync(path.join(host.HOME,'.ssh'));fs.writeFileSync(path.join(host.HOME,'.ssh/config'),'Host fixture\n');
  for(const key of ['USERPROFILE','XDG_CONFIG_HOME','APPDATA','LOCALAPPDATA'])fs.writeFileSync(path.join(host[key],'tool-config'),'host '+key);
  return {root,folder,cache,data:path.join(root,'private data'),host};
}
async function port() { const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p; }
function run(t,f,editor,p,id='a'.repeat(32),extra={}) {
  // Explicit fake host roots and fake tool paths: never use real home files or
  // propagate inherited omo identity. Mixed-case selectors model Windows input.
  const capture=path.join(f.root,'capture-'+id+'.json');
  const runtime=f.runtime??fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'omo-r-')));
  if(!f.runtime)t.after(()=>fs.rmSync(runtime,{recursive:true,force:true}));
  const child=spawn(process.execPath,[launch,'run',editor,f.cache,f.folder,f.data,id,String(p),f.base??'/proxy/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/',runtime],{env:{PATH:process.env.PATH,...f.host,GIT_CONFIG_NOSYSTEM:'1',SSH_AUTH_SOCK:path.join(f.root,'fake-agent.sock'),GIT_SSH_COMMAND:'ssh -F fake-tool-config',CAPTURE_FILE:capture,PORT:'9',CODE_SERVER_HOST:'0.0.0.0',code_server_config:path.join(f.root,'foreign-config.yaml'),VSCODE_PORTABLE:path.join(f.root,'foreign-portable'),vscode_appdata:path.join(f.root,'foreign-appdata'),VSCODE_AGENT_FOLDER:path.join(f.root,'foreign-server'),NODE_OPTIONS:'',TMPDIR:path.join(f.root,'inherited long temp directory'),TEMP:path.join(f.root,'inherited windows temp'),EDITOR_HOST_XDG_RUNTIME_DIR:'foreign-host-runtime',editor_host_xdg_runtime_dir:'foreign-case-runtime',CS_DISABLE_PROXY:'1',EXTENSIONS_GALLERY:'https://foreign.invalid/gallery',tmp:'foreign-tmp',TmpDir:'foreign-temp',PASSWORD:'secret',OMO_FAKE_SECRET:'must-not-reach-editor',...extra}});
  let output='';child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>output+=b);
  const end=new Promise(r=>child.on('close',(code,signal)=>r({code,signal,output})));
  const stop=async()=>{child.kill('SIGTERM');await end;};
  t.after(stop);return {child,end,stop,capture,runtime,output:()=>output};
}
async function until(fn) { for(let i=0;i<200;i++){if(fn())return;await new Promise(r=>setTimeout(r,20));}assert.fail('condition timed out'); }
const editorScript=`
const fs=require('node:fs'),http=require('node:http'),path=require('node:path'),cp=require('node:child_process');
const args=process.argv.slice(2), find=k=>args.includes(k)?args[args.indexOf(k)+1]:undefined;
const toolConfig={git:cp.execFileSync('git',['config','--global','--get','omo.launcherFixture'],{encoding:'utf8'}).trim(),ssh:fs.readFileSync(path.join(process.env.HOME,'.ssh/config'),'utf8')};
for(const key of ['USERPROFILE','XDG_CONFIG_HOME','APPDATA','LOCALAPPDATA'])toolConfig[key]=fs.readFileSync(path.join(process.env[key],'tool-config'),'utf8');
let publisherPaths;
if(find('--bind-addr')){publisherPaths=require('./util.js').paths;for(const name of ['data','config','runtime']){fs.mkdirSync(publisherPaths[name],{recursive:true});fs.writeFileSync(path.join(publisherPaths[name],'publisher-'+name),'fixture');}}
let ipcPath;
if(process.env.EDITOR_FIXTURE_IPC_PROBE){const net=require('node:net');ipcPath=path.join(process.env.XDG_RUNTIME_DIR,'vscode-ipc-11111111-1111-4111-8111-111111111111.sock');const ipc=net.createServer(socket=>socket.end(toolConfig.git));ipc.on('error',error=>{console.error(error.code);process.exit(24)});ipc.listen(ipcPath);}
fs.writeFileSync(process.env.CAPTURE_FILE,JSON.stringify({args,env:process.env,cwd:process.cwd(),toolConfig,publisherPaths,ipcPath}));
const addr=find('--bind-addr'),port=addr?Number(addr.split(':')[1]):Number(find('--port'));
const server=http.createServer((q,s)=>s.end('fixture editor'));
server.on('error',e=>{console.error(e.code);process.exit(1)});
server.listen(port,'127.0.0.1',()=>console.log(addr?'HTTP server listening on http://127.0.0.1:'+port:'Server bound to 127.0.0.1:'+port+' (IPv4)'));
`;
function flag(capture,key){return capture.args[capture.args.indexOf(key)+1];}
function vscodeFlag(capture,key,editor){
  if(editor==='openvscode')return flag(capture,key);
  const prefix=key.replace(/^--/,'')+'=';
  return capture.args.find((value,i)=>capture.args[i-1]==='--vscode-option'&&value.startsWith(prefix))?.slice(prefix.length);
}
for(const editor of ['code-server','openvscode'])for(const base of ['/', '/proxy/0123456789abcdef0123456789abcdef/'])for(const windows of [false,true])test(editor+' base-path contract '+base+(windows?' on Windows branch':''),async t=>{
  const f=setup(t,editorScript,editor);f.base=base;
  const extra={};
  if(windows){
    const platform=path.join(f.root,'windows-platform.cjs');
    fs.writeFileSync(platform,"Object.defineProperty(process,'platform',{value:'win32'});");
    extra.NODE_OPTIONS='--require '+JSON.stringify(platform);
  }
  const p=await port(),r=run(t,f,editor,p,'d'.repeat(32),extra);
  await until(()=>r.output().includes('OMO_EDITOR_READY'));
  const capture=JSON.parse(fs.readFileSync(r.capture));
  const count=capture.args.filter(arg=>arg==='--server-base-path').length;
  if(editor==='openvscode'&&base!=='/'){
    assert.equal(count,1);assert.equal(flag(capture,'--server-base-path'),base);
  }else assert.equal(count,0,'root must use the publisher default; explicit / breaks OpenVSCode WebSocket URLs');
  assert.equal(await(await fetch('http://127.0.0.1:'+p+base)).text(),'fixture editor');
});
for(const editor of ['code-server','openvscode']) test(editor+' preserves fake host tools and confines editor state with literal workspace and loopback',async t=>{
  const f=setup(t,editorScript,editor),p=await port(),r=run(t,f,editor,p);
  await until(()=>r.output().includes('OMO_EDITOR_READY'));
  const capture=JSON.parse(fs.readFileSync(r.capture));
  for(const k of ['PORT','CODE_SERVER_HOST','code_server_config','vscode_appdata','PASSWORD','OMO_FAKE_SECRET','NODE_OPTIONS','CS_DISABLE_PROXY','EXTENSIONS_GALLERY','tmp','TmpDir'])assert.equal(capture.env[k],undefined,k);
  for(const [k,v]of Object.entries(f.host))if(k!=='XDG_RUNTIME_DIR')assert.equal(capture.env[k],v,k);
  assert.equal(capture.env.SSH_AUTH_SOCK,path.join(f.root,'fake-agent.sock'));assert.equal(capture.env.GIT_SSH_COMMAND,'ssh -F fake-tool-config');
  assert.equal(capture.toolConfig.git,'host-config-readable');assert.equal(capture.toolConfig.ssh,'Host fixture\n');
  for(const key of ['USERPROFILE','XDG_CONFIG_HOME','APPDATA','LOCALAPPDATA'])assert.equal(capture.toolConfig[key],'host '+key);
  for(const k of ['TMPDIR','TEMP','TMP','XDG_RUNTIME_DIR'])assert.equal(capture.env[k],r.runtime,k);
  for(const k of ['VSCODE_AGENT_FOLDER','VSCODE_CODE_CACHE_PATH','VSCODE_PORTABLE'])assert.ok(capture.env[k].startsWith(f.data+path.sep),k);
  const user=flag(capture,'--user-data-dir'),extensions=flag(capture,'--extensions-dir');
  for(const dir of [user,extensions,vscodeFlag(capture,'--server-data-dir',editor),vscodeFlag(capture,'--logsPath',editor),vscodeFlag(capture,'--extensions-download-dir',editor)])assert.ok(dir?.startsWith(f.data+path.sep),String(dir));
  assert.equal(user,path.join(capture.env.VSCODE_PORTABLE,'user-data'));
  assert.ok(capture.args.includes(f.folder));assert.equal(capture.cwd,f.folder);
  if(editor==='code-server'){
    assert.equal(flag(capture,'--auth'),'none');assert.ok(capture.args.includes('--disable-update-check'));assert.ok(capture.args.includes('--trusted-origins'));
    assert.equal(flag(capture,'--bind-addr'),'127.0.0.1:'+p);
    assert.ok(flag(capture,'--session-socket').startsWith(r.runtime+path.sep)||flag(capture,'--session-socket').startsWith('\\\\.\\pipe\\omo-editor-'));
    assert.ok(flag(capture,'--config').startsWith(f.data+path.sep));
    for(const dir of [capture.publisherPaths.data,capture.publisherPaths.config])assert.ok(dir.startsWith(f.data+path.sep),dir);
    assert.equal(capture.publisherPaths.runtime,r.runtime);
    assert.equal(fs.existsSync(path.join(f.host.XDG_DATA_HOME,'code-server')),false);
  }else{
    assert.ok(capture.args.includes('--without-connection-token'));assert.equal(flag(capture,'--server-base-path'),'/proxy/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/');
    assert.equal(flag(capture,'--default-folder'),f.folder);assert.equal(flag(capture,'--host'),'127.0.0.1');
  }
  assert.equal(JSON.parse(fs.readFileSync(path.join(user,'Machine','settings.json')))['update.mode'],'none');
  const response=await fetch('http://127.0.0.1:'+p);assert.equal(await response.text(),'fixture editor');
});
for(const editor of ['code-server','openvscode'])test(editor+' shares edited profile/extensions across concurrent folders and artifact versions but separates runtime',async t=>{
  const f=setup(t,editorScript,editor),first=run(t,f,editor,await port());
  await until(()=>first.output().includes('OMO_EDITOR_READY'));
  const a=JSON.parse(fs.readFileSync(first.capture)),user=flag(a,'--user-data-dir'),extensions=flag(a,'--extensions-dir');
  const settings='// JSONC user edits survive\n{"editor.fontSize": 19}\n';
  const machineSettings='// Shared remote settings\n{"terminal.integrated.env.linux":{"MY_TOOL_CONFIG":"retained"}}\n';
  fs.writeFileSync(path.join(user,'Machine','settings.json'),machineSettings);
  fs.writeFileSync(path.join(user,'User','settings.json'),settings);fs.writeFileSync(path.join(extensions,'installed-extension'),'user extension');
  const secondArtifact=setup(t,editorScript,editor);secondArtifact.data=f.data;secondArtifact.host=f.host;
  const second=run(t,secondArtifact,editor,await port(),'b'.repeat(32));await until(()=>second.output().includes('OMO_EDITOR_READY'));
  const b=JSON.parse(fs.readFileSync(second.capture));
  assert.equal(flag(b,'--user-data-dir'),user);assert.equal(flag(b,'--extensions-dir'),extensions);
  assert.equal(fs.readFileSync(path.join(user,'Machine','settings.json'),'utf8'),machineSettings);assert.equal(fs.readFileSync(path.join(user,'User','settings.json'),'utf8'),settings);assert.equal(fs.readFileSync(path.join(extensions,'installed-extension'),'utf8'),'user extension');
  for(const k of ['TMPDIR','TEMP','TMP','XDG_RUNTIME_DIR','VSCODE_AGENT_FOLDER','VSCODE_CODE_CACHE_PATH'])assert.notEqual(a.env[k],b.env[k],k);
  for(const k of ['--server-data-dir','--logsPath'])assert.notEqual(vscodeFlag(a,k,editor),vscodeFlag(b,k,editor),k);
  if(editor==='code-server'){assert.notEqual(flag(a,'--session-socket'),flag(b,'--session-socket'));assert.notEqual(a.publisherPaths.runtime,b.publisherPaths.runtime);}
  assert.equal(await(await fetch('http://127.0.0.1:'+flag(a,editor==='code-server'?'--bind-addr':'--port').split(':').at(-1))).text(),'fixture editor');
});
for(const editor of ['code-server','openvscode'])test(editor+' applies Windows selector and temporary-path safeguards without replacing host tool roots',async t=>{
  const f=setup(t,editorScript,editor),platform=path.join(f.root,'windows-platform.cjs');
  // Exercise the launcher's native Windows branch using an isolated startup
  // module. This is a flag/environment fixture, not a native Windows claim.
  fs.writeFileSync(platform,"Object.defineProperty(process,'platform',{value:'win32'});");
  const r=run(t,f,editor,await port(),'c'.repeat(32),{NODE_OPTIONS:'--require '+JSON.stringify(platform)});
  await until(()=>r.output().includes('OMO_EDITOR_READY'));
  const capture=JSON.parse(fs.readFileSync(r.capture));
  for(const [key,value]of Object.entries(f.host))if(key!=='XDG_RUNTIME_DIR')assert.equal(capture.env[key],value,key);
  for(const key of ['tmp','TmpDir','code_server_config','vscode_appdata','CODE_SERVER_HOST','NODE_OPTIONS'])assert.equal(capture.env[key],undefined,key);
  assert.equal(capture.env.TMP,r.runtime);assert.equal(capture.env.TEMP,capture.env.TMP);
  assert.equal(capture.toolConfig.git,'host-config-readable');
  if(editor==='code-server')assert.match(flag(capture,'--session-socket'),/^\\\\\.\\pipe\\omo-editor-[a-f0-9]{24}$/);
});
test('Windows pipes preserve case-sensitive roots and change with each runtime lease',async t=>{
  const f=setup(t,editorScript,'code-server'),platform=path.join(f.root,'windows-platform.cjs');
  fs.writeFileSync(platform,"Object.defineProperty(process,'platform',{value:'win32'});");
  const extra={NODE_OPTIONS:'--require '+JSON.stringify(platform)};
  // Case-sensitive Windows directories can have distinct paths that fold to
  // the same string. Lease identity must survive that distinction as well.
  const upper=path.join(f.root,'Lease'),lower=path.join(f.root,'lease'),next=path.join(f.root,'Lease-next');
  fs.mkdirSync(upper);fs.mkdirSync(lower);fs.mkdirSync(next);
  const upperState={...f,data:path.join(f.root,'State'),runtime:upper};
  const lowerState={...f,data:path.join(f.root,'state'),runtime:lower};
  const firstPort=await port(),first=run(t,upperState,'code-server',firstPort,'a'.repeat(32),extra);
  await until(()=>first.output().includes('OMO_EDITOR_READY'));
  const a=JSON.parse(fs.readFileSync(first.capture));
  const secondPort=await port(),second=run(t,lowerState,'code-server',secondPort,'a'.repeat(32),extra);
  await until(()=>second.output().includes('OMO_EDITOR_READY'));
  const b=JSON.parse(fs.readFileSync(second.capture));
  assert.notEqual(flag(a,'--session-socket'),flag(b,'--session-socket'));
  await first.stop();
  assert.equal(await(await fetch('http://127.0.0.1:'+secondPort)).text(),'fixture editor');
  assert.equal(second.child.exitCode,null);
  assert.equal(second.child.signalCode,null);
  const restarted=run(t,{...upperState,runtime:next},'code-server',await port(),'a'.repeat(32),extra);
  await until(()=>restarted.output().includes('OMO_EDITOR_READY'));
  assert.notEqual(flag(JSON.parse(fs.readFileSync(restarted.capture)),'--session-socket'),flag(a,'--session-socket'));
});
test('editor selections keep separate profiles and extensions in one plugin data folder',async t=>{
  const code=setup(t,editorScript,'code-server'),open=setup(t,editorScript,'openvscode');open.data=code.data;
  const a=run(t,code,'code-server',await port()),b=run(t,open,'openvscode',await port(),'b'.repeat(32));
  await until(()=>a.output().includes('OMO_EDITOR_READY')&&b.output().includes('OMO_EDITOR_READY'));
  const first=JSON.parse(fs.readFileSync(a.capture)),second=JSON.parse(fs.readFileSync(b.capture));
  for(const key of ['--user-data-dir','--extensions-dir'])assert.notEqual(flag(first,key),flag(second,key),key);
});
for(const editor of ['code-server','openvscode'])test(editor+' uses a leased short IPC runtime even under long Unicode plugin data and inherited temp overrides',async t=>{
  const f=setup(t,editorScript,editor);f.data=path.join(f.root,'世界'.repeat(30),'persistent editor data');
  const r=run(t,f,editor,await port(),'d'.repeat(32),{EDITOR_FIXTURE_IPC_PROBE:'1'});
  await until(()=>r.output().includes('OMO_EDITOR_READY'));
  const capture=JSON.parse(fs.readFileSync(r.capture));
  assert.equal(capture.env.XDG_RUNTIME_DIR,r.runtime);assert.equal(capture.env.TMPDIR,r.runtime);
  assert.ok(Buffer.byteLength(capture.ipcPath,'utf8')<=107);
  const echo=await new Promise((resolve,reject)=>{let data='';const client=net.connect(capture.ipcPath);client.on('data',chunk=>data+=chunk);client.on('end',()=>resolve(data));client.on('error',reject);});
  assert.equal(echo,'host-config-readable');assert.ok(flag(capture,'--user-data-dir').startsWith(f.data+path.sep));
  assert.equal(fs.readdirSync(f.host.XDG_RUNTIME_DIR).length,0);
});
for(const editor of ['code-server','openvscode'])test(editor+' refuses an oversized multibyte Unix runtime before starting the editor',async t=>{
  const f=setup(t,"console.log('EDITOR_STARTED');process.exit(0)",editor);f.runtime=path.join(f.root,'界'.repeat(10));fs.mkdirSync(f.runtime);
  const longest=path.join(f.runtime,'vscode-ipc-11111111-1111-4111-8111-111111111111.sock');
  assert.ok(longest.length<=107);assert.ok(Buffer.byteLength(longest,'utf8')>107);
  const result=await run(t,f,editor,await port()).end;
  assert.notEqual(result.code,0);assert.match(result.output,/runtime.*too long.*short.*temporary/i);assert.ok(!result.output.includes('EDITOR_STARTED'));
});
for(const editor of ['code-server','openvscode'])test(editor+' initializes supported terminal host-runtime restoration without trusting inherited restore selectors',async t=>{
  for(const present of [true,false]){
    const f=setup(t,editorScript,editor),r=run(t,f,editor,await port(),'e'.repeat(32),present?{}:{XDG_RUNTIME_DIR:undefined});
    await until(()=>r.output().includes('OMO_EDITOR_READY'));
    const capture=JSON.parse(fs.readFileSync(r.capture));
    assert.equal(capture.env.EDITOR_HOST_XDG_RUNTIME_DIR,present?f.host.XDG_RUNTIME_DIR:'');assert.equal(capture.env.editor_host_xdg_runtime_dir,undefined);
    const settings=JSON.parse(fs.readFileSync(path.join(flag(capture,'--user-data-dir'),'Machine','settings.json')));
    for(const platform of ['linux','osx'])assert.deepEqual(settings['terminal.integrated.env.'+platform],{XDG_RUNTIME_DIR:'${env:EDITOR_HOST_XDG_RUNTIME_DIR}'});
    assert.equal(capture.env.XDG_RUNTIME_DIR,r.runtime);
  }
});
for(const editor of ['code-server','openvscode'])test(editor+' initializes missing remote Machine settings from existing JSONC without replacing user edits',async t=>{
  const f=setup(t,editorScript,editor),user=path.join(f.data,'profiles',editor,'portable','user-data'),settings='// Existing profile settings\n{"terminal.integrated.env.linux":{"CUSTOM_TOOL":"existing"}}\n';
  fs.mkdirSync(path.join(user,'User'),{recursive:true});fs.writeFileSync(path.join(user,'User','settings.json'),settings);
  const r=run(t,f,editor,await port());await until(()=>r.output().includes('OMO_EDITOR_READY'));
  assert.equal(fs.readFileSync(path.join(user,'Machine','settings.json'),'utf8'),settings);assert.equal(fs.readFileSync(path.join(user,'User','settings.json'),'utf8'),settings);
});
test('macOS uses its stricter Unix socket-byte limit while Linux accepts the same literal runtime path',async t=>{
  const f=setup(t,"console.log('EDITOR_STARTED');process.exit(0)",'openvscode');
  f.runtime=path.join(f.root,'r'.repeat(105-f.root.length-1-53));fs.mkdirSync(f.runtime);
  assert.equal(Buffer.byteLength(path.join(f.runtime,'vscode-ipc-11111111-1111-4111-8111-111111111111.sock'),'utf8'),105);
  for(const platform of ['linux','darwin']){
    const startup=path.join(f.root,platform+'.cjs');fs.writeFileSync(startup,"Object.defineProperty(process,'platform',{value:"+JSON.stringify(platform)+"});");
    const result=await run(t,f,'openvscode',await port(),'f'.repeat(32),{NODE_OPTIONS:'--require '+JSON.stringify(startup)}).end;
    if(platform==='linux'){assert.equal(result.code,0);assert.match(result.output,/EDITOR_STARTED/);}
    else{assert.notEqual(result.code,0);assert.match(result.output,/runtime.*too long.*short.*temporary/i);assert.ok(!result.output.includes('EDITOR_STARTED'));}
  }
});
test('early child exit never reports readiness',async t=>{
 const f=setup(t,"console.error('fixture ABI failure');process.exit(23)");const r=await run(t,f,'code-server',await port()).end;
 assert.equal(r.code,23);assert.match(r.output,/ABI failure/);assert.ok(!r.output.includes('OMO_EDITOR_READY'));
});
test('port collision is classified without trusting another listener',async t=>{
 const s=net.createServer(c=>c.end('not editor'));await new Promise(r=>s.listen(0,'127.0.0.1',r));t.after(()=>s.close());
 const f=setup(t,editorScript);const r=await run(t,f,'code-server',s.address().port).end;
 assert.notEqual(r.code,0);assert.match(r.output,/OMO_EDITOR_PORT_COLLISION/);assert.ok(!r.output.includes('OMO_EDITOR_READY'));
});
test('identity has one unambiguous hex marker despite Node startup warnings and preload stdout',async t=>{
 const f=setup(t,'');const alias=path.join(f.root,'alias');fs.symlinkSync(f.folder,alias,'dir');
 const preload=path.join(f.root,'startup.cjs');fs.writeFileSync(preload,"process.stdout.write('startup stdout without newline');process.emitWarning('fixture startup warning');");
 for(const folder of [f.folder,alias]){
  const c=spawn(process.execPath,[launch,'identity',folder],{env:{PATH:process.env.PATH,NODE_OPTIONS:'--require '+JSON.stringify(preload)}});let output='',error='';c.stdout.on('data',b=>output+=b);c.stderr.on('data',b=>error+=b);assert.equal(await new Promise(r=>c.on('close',r)),0);
  const lines=output.split('\n').filter(line=>line.startsWith('OMO_EDITOR_IDENTITY:'));assert.equal(lines.length,1);
  const hex=lines[0].slice('OMO_EDITOR_IDENTITY:'.length);assert.match(hex,/^(?:[a-f0-9]{2})+$/);assert.equal(Buffer.from(hex,'hex').toString('utf8'),fs.realpathSync(f.folder));
  assert.match(output,/startup stdout without newline\nOMO_EDITOR_IDENTITY:/);assert.match(error,/fixture startup warning/);
 }
});
test('Microsoft cannot be selected through launcher entry or legacy consent',async t=>{
 const f=setup(t,"process.exit(0)");const r=await run(t,f,'microsoft',await port()).end;
 assert.notEqual(r.code,0);assert.match(r.output,/unavailable/);
});
