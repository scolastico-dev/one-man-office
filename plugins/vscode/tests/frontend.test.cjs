const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const path=require('node:path');const vm=require('node:vm');
function load(responses, now=()=>Date.now()){
 const buttons=[],panels=[],calls=[],removed=[],tabs=[];const controller=new AbortController();let cleanup;
 const facade={signal:controller.signal,initialize:fn=>{cleanup=fn()},addSidebarTab:t=>tabs.push(t),addButton:b=>buttons.push(b),addPanel:p=>panels.push(p),removePanel:id=>removed.push(id),
   runHook:async(action,context)=>{calls.push({action,context});const value=typeof responses==='function'?responses(action,context):responses.shift();if(value instanceof Error)throw value;return value},
   openForward:()=>assert.fail('button callback must return final result to reserved tab')};
 const timers=new Map();let sequence=0;
 const ctx=vm.createContext({document:{currentScript:{omo:facade}},DOMException,Error,Promise,Map,Date:class extends Date {static now(){return now()}},
   setTimeout:(fn)=>{const id=++sequence;timers.set(id,fn);return id},clearTimeout:id=>timers.delete(id)});
 vm.runInContext(fs.readFileSync(path.join(__dirname,'../frontend.js'),'utf8'),ctx);
 return {buttons,panels,calls,removed,tabs,controller,cleanup:()=>cleanup?.(),tick:async()=>{await new Promise(r=>setImmediate(r));for(const [id,fn]of timers){timers.delete(id);fn();break}await new Promise(r=>setImmediate(r))}};
}
test('first click waits through install and startup and returns final forward for reserved tab',async()=>{
 const f=load([{message:'VS Code pending: downloading 25%'},{message:'VS Code pending: starting'},{message:'Ready',forward_id:'a'.repeat(32)}]);
 assert.equal(f.buttons.length,2);const button=f.buttons.find(b=>b.place==='ticket');assert.equal(button.label,'Open in VS Code');assert.equal(button.per_worktree,true);
 const promise=button.run({ticket:{number:7},project:'second'});await f.tick();await f.tick();const result=await promise;
 assert.equal(result.forward_id,'a'.repeat(32));assert.deepEqual(f.calls.map(c=>c.action),['open-ticket','status-ticket','status-ticket']);
 assert.equal(f.calls[0].context.ticket,7);assert.equal(f.calls[0].context.project,'second');assert.ok(f.panels.some(p=>p.text.includes('25%')));assert.ok(f.removed.length);
});
test('project context never inherits an open ticket and message failures stop polling',async()=>{
 const f=load([{message:'Microsoft terms have not been confirmed'}]);
 assert.deepEqual(JSON.parse(JSON.stringify(f.tabs)),[{id:'editor',label:'VS Code',tasks:true}]);
 assert.equal(f.buttons.filter(b=>b.place==='project').length,0);
 const button=f.buttons.find(b=>b.place==='sidebar');assert.equal(button.tab,'editor');assert.equal(button.pick_project,true);
 const r=await button.run({ticket:{number:9},project:'alpha'});
 assert.match(r.message,/terms/);assert.equal(f.calls[0].action,'open-project');assert.equal(f.calls[0].context.ticket,undefined);assert.equal(f.calls.length,1);
});
test('lost start response is surfaced without automatic retry',async()=>{
 const f=load([new Error('connection lost')]);await assert.rejects(f.buttons[0].run({ticket:{number:1},project:'alpha'}),/connection lost/);assert.equal(f.calls.length,1);
});
test('disabled frontend aborts polling and cannot navigate late',async()=>{
 const f=load([{message:'VS Code pending: downloading'},{message:'Ready',forward_id:'b'.repeat(32)}]);const p=f.buttons[0].run({ticket:{number:1},project:'alpha'});
 await new Promise(r=>setImmediate(r));f.controller.abort();await assert.rejects(p,/disabled|abort/i);assert.equal(f.calls.length,1);
});
test('same-document concurrent clicks share pending work',async()=>{
 const f=load([{message:'VS Code pending: installing'},{message:'Ready',forward_id:'a'.repeat(32)}]);const context={ticket:{number:7},project:'alpha'};
 const a=f.buttons[0].run(context),b=f.buttons[0].run(context);await f.tick();assert.equal((await a).forward_id,(await b).forward_id);assert.equal(f.calls.filter(c=>c.action==='open-ticket').length,1);
});

function apiError(status,code,message){return Object.assign(new Error(message),{status,code})}
const busy=()=>apiError(409,'conflict','an action of plugin vscode is already running');

test('a busy open retries the same folder and returns its final reserved-tab result',async()=>{
 const f=load([busy(),{message:'VS Code pending: installing'},{message:'Ready',forward_id:'c'.repeat(32)}]);
 const p=f.buttons[0].run({ticket:{number:7},project:'second'});
 // Observe rejection immediately while advancing the deterministic timers.
 const result=p.then(value=>({value}),error=>({error}));
 await f.tick();await f.tick();const outcome=await result;
 assert.equal(outcome.error,undefined);assert.equal(outcome.value.forward_id,'c'.repeat(32));
 assert.deepEqual(f.calls.map(c=>c.action),['open-ticket','open-ticket','status-ticket']);
 assert.ok(f.calls.every(c=>c.context.ticket===7&&c.context.project==='second'));
 assert.ok(f.panels.some(p=>p.text.includes('installing')));
});

test('a busy status keeps polling without reissuing open or losing install progress',async()=>{
 const f=load([{message:'VS Code pending: downloading 35%'},busy(),{message:'Ready',forward_id:'d'.repeat(32)}]);
 const result=f.buttons[1].run({project:'alpha'}).then(value=>({value}),error=>({error}));
 await f.tick();assert.equal(f.removed.length,0);assert.ok(f.panels.some(p=>p.text.includes('35%')));
 assert.ok(f.panels.every(p=>p.place==='sidebar'&&p.tab==='editor'));
 await f.tick();const outcome=await result;
 assert.equal(outcome.error,undefined);assert.equal(outcome.value.forward_id,'d'.repeat(32));
 assert.deepEqual(f.calls.map(c=>c.action),['open-project','status-project','status-project']);
});

test('separate folders can wait for the same plugin busy lock without sharing results',async()=>{
 let betaCalls=0;const f=load((action,context)=>{
  if(context.project==='alpha')return action==='open-ticket'?{message:'VS Code pending: starting'}:{message:'Ready',forward_id:'e'.repeat(32)};
  return ++betaCalls===1?busy():{message:'Ready',forward_id:'f'.repeat(32)};
 });
 const first=f.buttons[0].run({ticket:{number:7},project:'alpha'}).then(value=>({value}),error=>({error}));
 const second=f.buttons[0].run({ticket:{number:7},project:'beta'}).then(value=>({value}),error=>({error}));
 await f.tick();await f.tick();const a=await first,b=await second;
 assert.equal(a.error,undefined);assert.equal(b.error,undefined);
 assert.equal(a.value.forward_id,'e'.repeat(32));assert.equal(b.value.forward_id,'f'.repeat(32));
 assert.deepEqual(f.calls.filter(c=>c.context.project==='alpha').map(c=>c.action),['open-ticket','status-ticket']);
 assert.deepEqual(f.calls.filter(c=>c.context.project==='beta').map(c=>c.action),['open-ticket','open-ticket']);
});

test('abort stops an open waiting for the busy hook lock',async()=>{
 const f=load([busy(),{message:'Ready'}]);const result=f.buttons[0].run({ticket:{number:1},project:'alpha'}).then(value=>({value}),error=>({error}));
 await new Promise(r=>setImmediate(r));f.controller.abort();const outcome=await result;
 assert.match(outcome.error.message,/disabled|abort/i);await f.tick();assert.equal(f.calls.length,1);
});

test('busy retries share the overall 35 minute deadline rather than resetting it',async()=>{
 let time=0;const f=load([busy(),{message:'Ready'}],()=>time);
 const result=f.buttons[0].run({ticket:{number:1},project:'alpha'}).then(value=>({value}),error=>({error}));
 await new Promise(r=>setImmediate(r));time=35*60*1000+1;await f.tick();const outcome=await result;
 assert.match(outcome.error.message,/timed out/i);assert.equal(f.calls.length,1);
});

for(const [label,error]of [
 ['another 409 conflict',apiError(409,'conflict','the selected folder is unavailable')],
 ['busy text without the busy status',apiError(422,'conflict','an action of plugin vscode is already running')],
 ['busy text without the conflict code',apiError(409,'validation','an action of plugin vscode is already running')],
 ['lost network response',apiError(0,'network','The connection was lost.')],
])test(label+' is never retried',async()=>{
 const f=load([error,{message:'Ready'}]);await assert.rejects(f.buttons[0].run({ticket:{number:1},project:'alpha'}),e=>e===error);assert.equal(f.calls.length,1);
});
