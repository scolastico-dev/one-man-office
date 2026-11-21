import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const source = readFileSync(new URL('../setup/app.js',import.meta.url),'utf8');
// Run the real setup against a form adapter and resolve on action()'s completed
// render, rather than waiting a guessed number of event-loop turns.
async function setup(state, replies = {}) {
  const elements = new Map(); const fields = new Map(); const calls=[]; let submit; let finish;
  const loaded = new Promise(resolve => { finish=resolve; });
  const button={set disabled(value){if(!value)finish();}};
  const local={remove(){this.removed=true}};
  const field=name=>{if(!fields.has(name))fields.set(name,{value:'',checked:false,querySelector(){return local}});return fields.get(name)};
  const listeners = new Map();
  const make = tag => ({tag,hidden:false,value:'',children:[],textContent:'',
    addEventListener(name,fn){this.listeners ??= new Map();this.listeners.set(name,fn)},
    replaceChildren(){this.children=[];this.value=''},
    append(...children){this.children.push(...children);if(this.tag==='select' && !this.value)this.value=children[0]?.value || ''},
    querySelectorAll(){return [button]}
  });
  const element=id=>{if(!elements.has(id)){const node=make(id==='forward-app'?'select':'div');node.addEventListener=(name,fn)=>listeners.set(id+':'+name,fn);elements.set(id,node)}return elements.get(id)};
  const form={elements:{namedItem:field},querySelectorAll(){return [button]},addEventListener(name,fn){if(name==='submit')submit=fn}};
  const document={querySelector(){return form},getElementById:element,createElement:make};
  const window={__TAURI__:{core:{async invoke(name,args){calls.push([name,args]);return Object.hasOwn(replies,name)?replies[name]:state}}},addEventListener(){}};
  vm.runInNewContext(source,{document,window});
  await loaded;
  return {field,element,local,calls,async click(id){
    const completed = new Promise(resolve => { finish=resolve; });
    listeners.get(id+':click')();await completed;
  },async clickNode(node){
    const completed = new Promise(resolve => { finish=resolve; });
    node.listeners.get('click')();await completed;
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
test('desktop forward controls require a connected server with negotiated tunnel support', async () => {
  for (const [android,connected,forward_tunnel,visible] of [[true,true,true,false],[false,false,true,false],[false,true,false,false],[false,true,true,true]]) {
    const {element}=await setup({android,connected,forward_tunnel,polling:false,error:''});
    assert.equal(element('forwards').hidden,!visible);
  }
});

test('registered apps open previews and expose chosen ports with visible status and Stop', async () => {
  const id='0123456789abcdef0123456789abcdef';
  const exposure={id:'local',name:'Editor',address:'http://127.0.0.1:8099/',preview:false,error:''};
  const state={android:false,connected:true,forward_tunnel:true,polling:false,error:''};
  const {element,calls,click,clickNode}=await setup(state, {
    get_forwards:{forwards:[{id,name:'Editor',ticket:7}],exposures:[]},
    preview_forward:[{...exposure,id:'preview',address:'http://editor.omo/',preview:true}],
    expose_forward:[exposure],stop_forward:[],
  });
  await click('refresh-forwards');
  assert.equal(element('forward-app').value,id);
  assert.match(element('forward-app').children[0].textContent,/Editor.*Ticket 7/);
  assert.equal(element('preview-forward').disabled,false);
  await click('preview-forward');
  assert.equal(calls.at(-1)[0],'preview_forward');
  assert.equal(calls.at(-1)[1].id,id);
  assert.match(element('forward-exposures').children[0].children[0].textContent,/http:\/\/editor.omo\//);
  element('forward-port').value='8099';await click('expose-forward');
  assert.equal(calls.at(-1)[0],'expose_forward');assert.equal(calls.at(-1)[1].port,'8099');
  const row=element('forward-exposures').children[0];
  assert.match(row.children[0].textContent,/127.0.0.1:8099.*Listening/);
  await clickNode(row.children[1]);assert.equal(calls.at(-1)[0],'stop_forward');
  assert.equal(element('forward-exposures').children.length,0);
});
test('registry outage retains local exposure status and End while reporting the registry error', async () => {
  const {element,click,clickNode,calls}=await setup({android:false,connected:true,forward_tunnel:true,error:''},{
    get_forwards:{forwards:[],error:'Cannot reach the board.',exposures:[{id:'local',name:'App',address:'http://127.0.0.1:8099/',preview:false,error:'Tunnel failed (HTTP 401).'}]},stop_forward:[],
  });
  await click('refresh-forwards');
  assert.equal(element('forward-problem').textContent,'Cannot reach the board.');
  assert.equal(element('forward-problem').hidden,false);
  const row=element('forward-exposures').children[0];
  assert.match(row.children[0].textContent,/401/);
  await clickNode(row.children[1]);
  assert.equal(calls.at(-1)[0],'stop_forward');
});
test('a new connection clears stale app choices and requires a fresh status read', async () => {
  const state={android:false,connected:true,forward_tunnel:true,forward_generation:'first',error:''};
  const {element,click,submit}=await setup(state,{
    connect:{...state,forward_generation:'second'},
    get_forwards:{forwards:[{id:'old',name:'Old app'}],exposures:[{id:'old-exposure',name:'Old app',address:'http://127.0.0.1:8099/',preview:false,error:''}]},
  });
  await click('refresh-forwards');assert.equal(element('forward-app').value,'old');
  await submit();
  assert.equal(element('forward-app').value,'');
  assert.equal(element('forward-exposures').children.length,0);
  assert.equal(element('preview-forward').disabled,true);
  assert.match(element('forward-exposure-status').textContent,/Refresh/);
});
