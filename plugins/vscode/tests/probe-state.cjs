'use strict';
const assert=require('node:assert/strict');

// Caller supplies the publisher launcher's canonical workspace. Neither API
// order nor another live folder can establish which process a tab belongs to.
function pinInstance(tasks,forwards,folder,known){
 if(known)assert.equal(known.folder,folder,'cached workspace identity must match');
 const matches=tasks.filter(t=>t.state==='running'&&(known?t.id===known.taskID:t.output.includes('Workspace: '+JSON.stringify(folder))));
 assert.equal(matches.length,1,'expected one running task for the exact workspace');
 const task=matches[0],attached=forwards.filter(f=>f.plugin==='vscode'&&f.task_id===task.id);
 assert.equal(attached.length,1,'expected one attached forward for the stable task');
 if(known)assert.equal(attached[0].id,known.forwardID,'cached forward identity must match');
 const marker=task.output.find(line=>line.startsWith('Runtime: '));
 const runtimePath=known?.runtimePath??(marker?JSON.parse(marker.slice(9)):undefined);
 return {folder,taskID:task.id,taskName:task.name,forwardID:attached[0].id,...(runtimePath?{runtimePath}:{})};
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
function freshSnapshot(before,after){
 return !!after&&typeof after.instance==='string'&&(!before||before.instance!==after.instance||after.sequence>before.sequence);
}
module.exports={pinInstance,isRunning,stopChild,freshSnapshot};
