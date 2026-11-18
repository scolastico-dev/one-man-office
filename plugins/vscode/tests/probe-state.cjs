'use strict';
const assert=require('node:assert/strict');

// Caller supplies the publisher launcher's canonical workspace. Neither API
// order nor another live folder can establish which process a tab belongs to.
function pinInstance(tasks,forwards,folder){
 const matches=tasks.filter(t=>t.state==='running'&&t.output.includes('Workspace: '+JSON.stringify(folder)));
 assert.equal(matches.length,1,'expected one running task for the exact workspace');
 const task=matches[0],attached=forwards.filter(f=>f.plugin==='vscode'&&f.task_id===task.id);
 assert.equal(attached.length,1,'expected one attached forward for the stable task');
 return {folder,taskID:task.id,taskName:task.name,forwardID:attached[0].id};
}
function isRunning(child){return !!child&&child.exitCode===null&&child.signalCode===null}
async function stopChild(child,graceMs=15000){
 if(!isRunning(child))return;
 await new Promise(resolve=>{
  const timer=setTimeout(()=>child.kill('SIGKILL'),graceMs);
  child.once('close',()=>{clearTimeout(timer);resolve()});
  child.kill('SIGTERM');
 });
}
module.exports={pinInstance,isRunning,stopChild};
