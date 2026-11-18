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
 const complete=await Promise.race([stopChild(child,20).then(()=>true),new Promise(r=>setTimeout(()=>r(false),100))]);
 assert.equal(complete,true,'cleanup waited for a close event that already happened');
});
