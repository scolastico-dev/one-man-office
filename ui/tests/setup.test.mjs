import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const source = readFileSync(new URL('../setup/app.js',import.meta.url),'utf8');
// Run the real setup against a form adapter and resolve on action()'s completed
// render, rather than waiting a guessed number of event-loop turns.
async function setup(state) {
  const elements = new Map(); const fields = new Map(); const calls=[]; let submit; let finish;
  const loaded = new Promise(resolve => { finish=resolve; });
  const button={set disabled(value){if(!value)finish();}};
  const local={remove(){this.removed=true}};
  const field=name=>{if(!fields.has(name))fields.set(name,{value:'',checked:false,querySelector(){return local}});return fields.get(name)};
  const element=id=>{if(!elements.has(id))elements.set(id,{hidden:false,addEventListener(){}});return elements.get(id)};
  const form={elements:{namedItem:field},querySelectorAll(){return [button]},addEventListener(name,fn){if(name==='submit')submit=fn}};
  const document={querySelector(){return form},getElementById:element};
  const window={__TAURI__:{core:{async invoke(name,args){calls.push([name,args]);return state}}},addEventListener(){}};
  vm.runInNewContext(source,{document,window});
  await loaded;
  return {field,element,local,calls,async submit(){
    const submitted = new Promise(resolve => { finish=resolve; });
    submit({preventDefault(){}});
    await submitted;
  }};
}
test('Android connection form exposes scheduling while excluding local lifecycle controls', async () => {
  const state={android:true,connected:false,polling:false,error:'',config:{mode:'direct',origin:'https://board.example',auth:'none',username:'',notifications:true,remember:false,notification_mode:'foreground',stop_with_window:true}};
  const {element,field,local,calls,submit}=await setup(state);
  assert.equal(element('notifications-label').hidden,false);
  assert.equal(element('notification-mode-label').hidden,false);
  assert.equal(field('notifications').checked,true);
  assert.equal(field('notification_mode').value,'foreground');
  assert.equal(local.removed,true);
  assert.equal(element('stop-with-window-label').hidden,true);
  assert.equal(element('close-hint').hidden,true);
  assert.equal(field('stop_with_window').checked,false);
  await submit();
  assert.equal(calls.at(-1)[0],'connect');
  assert.equal(calls.at(-1)[1].settings.notification_mode,'foreground');
  assert.equal(calls.at(-1)[1].settings.stop_with_window,false);
});
test('desktop local connection retains notification and owned-server close settings', async () => {
  const state={android:false,connected:false,polling:false,error:'',config:{mode:'local',origin:'http://localhost:8090',auth:'token',username:'',notifications:true,remember:true,notification_mode:'background',stop_with_window:true}};
  const {element,field,local,calls,submit}=await setup(state);
  assert.equal(local.hidden,false);
  assert.equal(local.disabled,false);
  assert.equal(element('notification-mode-label').hidden,true);
  assert.equal(element('stop-with-window-label').hidden,false);
  assert.equal(field('stop_with_window').checked,true);
  await submit();
  const [command,{settings}]=calls.at(-1);
  assert.equal(command,'local_connect');
  assert.equal(settings.notifications,true);
  assert.equal(settings.remember,true);
  assert.equal(settings.stop_with_window,true);
});
