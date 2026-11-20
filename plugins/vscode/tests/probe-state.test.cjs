'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {pinInstance}=require('./probe-state.cjs');
const task=(id,folder,state='running')=>({id,name:'run-'+id,state,output:['Workspace: '+JSON.stringify(folder)]});
const forward=(id,task_id,plugin='vscode')=>({id,task_id,plugin});

test('publisher probe pins the exact workspace and attached stable IDs regardless of list order',()=>{
 const folder='/tmp/second " 世界';
 const tasks=[task('old',folder,'exited'),task('first','/tmp/first'),task('second',folder)];
 const forwards=[forward('second-forward','second'),forward('first-forward','first')];
 const wanted={folder,taskID:'second',taskName:'run-second',forwardID:'second-forward'};
 assert.deepEqual(pinInstance(tasks,forwards,folder),wanted);
 assert.deepEqual(pinInstance(tasks.reverse(),forwards.reverse(),folder),wanted);
});

test('publisher probe refuses missing or ambiguous workspace/task/forward ownership',()=>{
 const folder='/tmp/workspace',tasks=[task('one',folder)],forwards=[forward('forward','one')];
 assert.throws(()=>pinInstance([task('other',folder+' extra')],forwards,folder),/one running task/);
 assert.throws(()=>pinInstance([...tasks,task('duplicate',folder)],forwards,folder),/one running task/);
 assert.throws(()=>pinInstance(tasks,[forward('wrong','different')],folder),/one attached forward/);
 assert.throws(()=>pinInstance(tasks,[forward('wrong-owner','one','other')],folder),/one attached forward/);
 assert.throws(()=>pinInstance(tasks,[...forwards,forward('duplicate','one')],folder),/one attached forward/);
});

const {spawn}=require('node:child_process');
const {once}=require('node:events');
const {isRunning,stopChild}=require('./probe-state.cjs');
async function childProcess(t){
 const child=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{env:{PATH:process.env.PATH},stdio:'ignore'});
 t.after(()=>{if(child.exitCode===null&&child.signalCode===null)child.kill('SIGKILL')});
 await once(child,'spawn');
 return child;
}
test('publisher probe stops a live child and repeated cleanup completes', {timeout:5000},async t=>{
 const child=await childProcess(t);
 assert.equal(isRunning(child),true);
 await stopChild(child,1000);
 assert.equal(isRunning(child),false);
 await stopChild(child,20);
});
test('publisher probe cleanup completes when its server was already closed by a signal', {timeout:5000},async t=>{
 const child=await childProcess(t),closed=once(child,'close');
 child.kill('SIGTERM');await closed;
 assert.equal(child.exitCode,null);assert.notEqual(child.signalCode,null);
 assert.equal(isRunning(child),false);
 await stopChild(child,20);
 assert.equal(isRunning(child),false,'cleanup observes the completed process without waiting for another close event');
});
test('publisher probe retains observed runtime and stable ownership after the task output tail rolls over',()=>{
 const folder='/fixture/workspace',original={...task('one',folder),output:['Workspace: '+JSON.stringify(folder),'Runtime: '+JSON.stringify('/fixture/runtime')]};
 const forwards=[forward('owned','one')],known=pinInstance([original],forwards,folder);
 assert.equal(known.runtimePath,'/fixture/runtime');
 const quiet={...original,output:Array.from({length:100},(_,i)=>'later editor output '+i)};
 assert.deepEqual(pinInstance([quiet],forwards,folder,known),known);
 assert.throws(()=>pinInstance([quiet],[forward('replacement','one')],folder,known),/forward identity/);
 assert.throws(()=>pinInstance([quiet],forwards,'/different/folder',known),/workspace identity/);
});
test('fresh probe responses recognize a new extension host even when its counter restarts',()=>{
 const {freshSnapshot}=require('./probe-state.cjs'),previous={instance:'first',sequence:51};
 assert.equal(freshSnapshot(previous,{instance:'second',sequence:1}),true);
 assert.equal(freshSnapshot(previous,{instance:'first',sequence:52}),true);
 assert.equal(freshSnapshot(previous,previous),false);
 assert.equal(freshSnapshot(previous,null),false);
});
test('preference probes accept publisher theme aliases only when the requested theme is actually rendered',()=>{
 const {preferencesMatch}=require('./probe-state.cjs');
 const expected={font:19,theme:'Light Modern'},actual={font:19,theme:'Default Light Modern'};
 assert.equal(preferencesMatch(actual,expected,['vs','vscode-theme-defaults-themes-light_modern-json']),true);
 assert.equal(preferencesMatch(actual,expected,['vs-dark','vscode-theme-defaults-themes-dark_modern-json']),false);
 assert.equal(preferencesMatch({...actual,font:22},expected,['vscode-theme-defaults-themes-light_modern-json']),false);
 assert.equal(preferencesMatch({...actual,theme:'Default Light+'},expected,['vscode-theme-defaults-themes-light_modern-json']),false);
});
