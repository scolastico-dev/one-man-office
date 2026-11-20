import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { test } from 'node:test';
import assert from 'node:assert/strict';
// Exercise setup with a small form adapter: Android must expose working mode input,
// submit it, and keep the desktop-only choice inaccessible.
test('Android connection form exposes and submits notification scheduling mode', async () => {
  const elements = new Map(); const fields = new Map(); const calls=[]; let submit; let finish;
  const loaded = new Promise(resolve => { finish=resolve; });
  const button={set disabled(value){if(!value)finish();}};
  const local={remove(){this.removed=true}};
  const field=name=>{if(!fields.has(name))fields.set(name,{value:'',checked:false,querySelector(){return local}});return fields.get(name)};
  const element=id=>{if(!elements.has(id))elements.set(id,{hidden:false,addEventListener(){}});return elements.get(id)};
  const state={android:true,connected:false,polling:false,error:'',config:{mode:'direct',origin:'https://board.example',auth:'none',username:'',notifications:true,remember:false,notification_mode:'foreground'}};
  const form={elements:{namedItem:field},querySelectorAll(){return [button]},addEventListener(name,fn){if(name==='submit')submit=fn}};
  const document={querySelector(){return form},getElementById:element};
  const window={__TAURI__:{core:{async invoke(name,args){calls.push([name,args]);return state}}},addEventListener(){}};
  vm.runInNewContext(readFileSync(new URL('../setup/app.js',import.meta.url),'utf8'),{document,window});
  // action() releases the button only after rendering the completed native call.
  await loaded;
  assert.equal(element('notifications-label').hidden,false);
  assert.equal(element('notification-mode-label').hidden,false);
  assert.equal(field('notifications').checked,true);
  assert.equal(field('notification_mode').value,'foreground');
  assert.equal(local.removed,true);
  const submitted = new Promise(resolve => { finish=resolve; });
  submit({preventDefault(){}});
  await submitted;
  assert.equal(calls.at(-1)[0],'connect');
  assert.equal(calls.at(-1)[1].settings.notification_mode,'foreground');
});
