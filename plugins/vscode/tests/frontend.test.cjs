const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const path=require('node:path');const vm=require('node:vm');
function load(responses){
 const buttons=[],panels=[],calls=[],removed=[];const controller=new AbortController();let cleanup;
 const facade={signal:controller.signal,initialize:fn=>{cleanup=fn()},addButton:b=>buttons.push(b),addPanel:p=>panels.push(p),removePanel:id=>removed.push(id),
   runHook:async(action,context)=>{calls.push({action,context});const value=responses.shift();if(value instanceof Error)throw value;return value},
   openForward:()=>assert.fail('button callback must return final result to reserved tab')};
 const timers=new Map();let sequence=0;
 const ctx=vm.createContext({document:{currentScript:{omo:facade}},DOMException,Error,Promise,Map,Date,
   setTimeout:(fn)=>{const id=++sequence;timers.set(id,fn);return id},clearTimeout:id=>timers.delete(id)});
 vm.runInContext(fs.readFileSync(path.join(__dirname,'../frontend.js'),'utf8'),ctx);
 return {buttons,panels,calls,removed,controller,cleanup:()=>cleanup?.(),tick:async()=>{await new Promise(r=>setImmediate(r));for(const [id,fn]of timers){timers.delete(id);fn();break}await new Promise(r=>setImmediate(r))}};
}
test('first click waits through install and startup and returns final forward for reserved tab',async()=>{
 const f=load([{message:'VS Code pending: downloading 25%'},{message:'VS Code pending: starting'},{message:'Ready',forward_id:'a'.repeat(32)}]);
 assert.equal(f.buttons.length,2);const button=f.buttons.find(b=>b.place==='ticket');assert.equal(button.label,'Open in VS Code');assert.equal(button.per_worktree,true);
 const promise=button.run({ticket:{number:7},project:'second'});await f.tick();await f.tick();const result=await promise;
 assert.equal(result.forward_id,'a'.repeat(32));assert.deepEqual(f.calls.map(c=>c.action),['open-ticket','status-ticket','status-ticket']);
 assert.equal(f.calls[0].context.ticket,7);assert.equal(f.calls[0].context.project,'second');assert.ok(f.panels.some(p=>p.text.includes('25%')));assert.ok(f.removed.length);
});
test('project context never inherits an open ticket and message failures stop polling',async()=>{
 const f=load([{message:'Microsoft terms have not been confirmed'}]);const r=await f.buttons.find(b=>b.place==='project').run({ticket:{number:9},project:'alpha'});
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
