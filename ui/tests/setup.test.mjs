import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const source = readFileSync(new URL('../setup/app.js',import.meta.url),'utf8');
// Run the real setup against a form adapter and resolve on action()'s completed
// render, rather than waiting a guessed number of event-loop turns.
async function setup(state) {
  const elements = new Map(); const fields = new Map(); const calls=[]; const handlers=new Map(); let submit; let finish; let update;
  const loaded = new Promise(resolve => { finish=resolve; });
  const button={set disabled(value){if(!value)finish();}};
  const local={remove(){this.removed=true}};
  const field=name=>{if(!fields.has(name))fields.set(name,{value:'',checked:false,querySelector(){return local}});return fields.get(name)};
  const element=id=>{if(!elements.has(id))elements.set(id,{hidden:false,addEventListener(name,fn){handlers.set(id,fn)}});return elements.get(id)};
  const form={elements:{namedItem:field},querySelectorAll(){return [button]},addEventListener(name,fn){if(name==='submit')submit=fn}};
  const document={querySelector(){return form},getElementById:element};
  const window={__TAURI__:{event:{listen(name,fn){update=fn}},core:{async invoke(name,args){calls.push([name,args]);return state}}},addEventListener(){}};
  vm.runInNewContext(source,{document,window});
  await loaded;
  return {field,element,local,calls,update(state){update({payload:state})},async click(id){
    const clicked=new Promise(resolve=>{finish=resolve;});handlers.get(id)();await clicked;
  },async submit(){
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

test('Android retains relay choice and submits separate relay settings', async () => {
  const state={android:true,connected:false,polling:false,error:'',config:{mode:'relay',origin:'https://relay.example',host_id:'office',auth:'none',username:'',notifications:true,remember:true,notification_mode:'background'},relay:{phase:'failed'}};
  const ui=await setup(state);
  assert.equal(ui.field('mode').value,'relay');
  assert.equal(ui.element('mode-label').hidden,false);
  assert.equal(ui.element('relay').hidden,false);
  assert.equal(ui.element('direct').disabled,true);
  ui.field('admission').value='relay-password';
  await ui.submit();
  const [command,{settings}]=ui.calls.at(-1);
  assert.equal(command,'relay_connect');
  assert.equal(settings.origin,'https://relay.example');
  assert.equal(settings.host_id,'office');
  assert.equal(settings.admission,'relay-password');
  assert.equal(settings.new_pair,false);
  assert.equal(ui.field('admission').value,'');
});
test('only proved pairing exposes PIN and full key is inside Advanced', async () => {
  const info={aid:'abcdefghijklmnopqrstuv',pin:'007026',public_key:'FULL_RAW_MLKEM_KEY'};
  const ui=await setup({android:false,connected:false,polling:false,error:'',relay:{phase:'awaiting_accept',pairing:info}});
  assert.equal(ui.element('pairing').hidden,false);
  assert.equal(ui.element('pair-pin').textContent,'007026');
  assert.equal(ui.element('pair-command').textContent,'omo relay pair accept abcdefghijklmnopqrstuv 007026');
  assert.equal(ui.element('pair-key-command').textContent,'omo relay pair accept abcdefghijklmnopqrstuv --public-key FULL_RAW_MLKEM_KEY');
  const html=readFileSync(new URL('../setup/index.html',import.meta.url),'utf8');
  assert.match(html,/<details[^>]*>[\s\S]*?<summary>Advanced<\/summary>[\s\S]*?id="pair-public-key"[\s\S]*?id="pair-key-command"[\s\S]*?<\/details>/);
  const failed=await setup({android:false,connected:false,polling:false,error:'',relay:{phase:'failed',pairing:null}});
  assert.equal(failed.element('pairing').hidden,true);
  assert.match(failed.element('relay-status').textContent,/explicit|new pairing/i);
});

test('failure events never retry pairing and a new ceremony requires its own user action',async()=>{
  const state={android:false,connected:false,error:'',config:{mode:'relay',origin:'https://relay.example',host_id:'office',auth:'none',remember:true},relay:{phase:'failed'}};
  const ui=await setup(state);
  const count=ui.calls.length;
  ui.update({...state,relay:{phase:'failed'}});
  assert.equal(ui.calls.length,count);
  await ui.click('new-pair');
  assert.equal(ui.calls.at(-1)[0],'relay_connect');
  assert.equal(ui.calls.at(-1)[1].settings.new_pair,true);
});
