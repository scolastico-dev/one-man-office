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
  return {root,folder,cache,data:path.join(root,'private data')};
}
async function port() { const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p; }
function run(t,f,editor,p) {
  // Deliberately explicit environment; never propagate inherited omo identity.
  const child=spawn(process.execPath,[launch,'run',editor,f.cache,f.folder,f.data,'a'.repeat(32),String(p),'/proxy/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/'],{env:{PATH:process.env.PATH,HOME:f.root,PORT:'9',CODE_SERVER_HOST:'0.0.0.0',PASSWORD:'secret',NODE_OPTIONS:'',OMO_FAKE_SECRET:'must-not-reach-editor'}});
  let output='';child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>output+=b);
  const end=new Promise(r=>child.on('close',(code,signal)=>r({code,signal,output})));
  t.after(async()=>{child.kill('SIGTERM');await end});return {child,end,output:()=>output};
}
async function until(fn) { for(let i=0;i<200;i++){if(fn())return;await new Promise(r=>setTimeout(r,20));}assert.fail('condition timed out'); }
const editorScript=`
const fs=require('node:fs'),http=require('node:http');
fs.writeFileSync(process.env.HOME+'/capture.json',JSON.stringify({args:process.argv.slice(2),env:process.env,cwd:process.cwd()}));
const args=process.argv.slice(2), find=k=>args.includes(k)?args[args.indexOf(k)+1]:undefined;
const addr=find('--bind-addr'),port=addr?Number(addr.split(':')[1]):Number(find('--port'));
const server=http.createServer((q,s)=>s.end('fixture editor'));
server.on('error',e=>{console.error(e.code);process.exit(1)});
server.listen(port,'127.0.0.1',()=>console.log(addr?'HTTP server listening on http://127.0.0.1:'+port:'Server bound to 127.0.0.1:'+port+' (IPv4)'));
`;
for(const editor of ['code-server','openvscode']) test(editor+' launches literal workspace, private state and loopback flags before readiness',async t=>{
  const f=setup(t,editorScript,editor),p=await port(),r=run(t,f,editor,p);
  await until(()=>r.output().includes('OMO_EDITOR_READY'));
  const capture=JSON.parse(fs.readFileSync(path.join(f.data,'sessions','a'.repeat(32),'home','capture.json')));
  assert.equal(capture.env.PORT,undefined);assert.equal(capture.env.CODE_SERVER_HOST,undefined);assert.equal(capture.env.PASSWORD,undefined);assert.equal(capture.env.OMO_FAKE_SECRET,undefined);
  for(const k of ['HOME','USERPROFILE','XDG_CONFIG_HOME','XDG_DATA_HOME','XDG_CACHE_HOME','TMPDIR','TEMP','TMP'])assert.ok(capture.env[k].startsWith(f.data),k);
  assert.ok(capture.args.includes(f.folder));assert.equal(capture.cwd,f.folder);
  if(editor==='code-server'){
    assert.ok(capture.args.includes('--auth'));assert.equal(capture.args[capture.args.indexOf('--auth')+1],'none');
    assert.ok(capture.args.includes('--disable-update-check'));assert.ok(capture.args.includes('--trusted-origins'));
    assert.equal(capture.args[capture.args.indexOf('--bind-addr')+1],'127.0.0.1:'+p);
  }else{
    assert.ok(capture.args.includes('--without-connection-token'));assert.equal(capture.args[capture.args.indexOf('--server-base-path')+1],'/proxy/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/');
    assert.equal(capture.args[capture.args.indexOf('--default-folder')+1],f.folder);
  }
  const user=path.join(f.data,'sessions','a'.repeat(32),'user','User','settings.json');
  assert.equal(JSON.parse(fs.readFileSync(user))['update.mode'],'none');
  const response=await fetch('http://127.0.0.1:'+p);assert.equal(await response.text(),'fixture editor');
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
test('identity resolves workspace aliases and preserves Unicode and quotes',async t=>{
 const f=setup(t,'');const alias=path.join(f.root,'alias');fs.symlinkSync(f.folder,alias,'dir');
 const c=spawn(process.execPath,[launch,'identity',alias],{env:{PATH:process.env.PATH}});let output='';c.stdout.on('data',b=>output+=b);await new Promise(r=>c.on('close',r));assert.equal(output,fs.realpathSync(f.folder));
});
test('Microsoft cannot be selected through launcher entry or legacy consent',async t=>{
 const f=setup(t,"process.exit(0)");const r=await run(t,f,'microsoft',await port()).end;
 assert.notEqual(r.code,0);assert.match(r.output,/unavailable/);
});
