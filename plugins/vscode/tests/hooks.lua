-- Boundary fixtures: real hook code; slow artifact/process/registry adapters
-- follow the published generic contracts. No publisher downloads here.
local source = assert(io.open('plugins/vscode/editor.lua')):read('*a')
local passed = 0
local function same(a,b) assert(a==b, tostring(a)..' ~= '..tostring(b)) end
local function contains(s,t) assert(string.find(s,t,1,true), tostring(s)..' lacks '..t) end
local function copy(v) if type(v)~='table' then return v end;local out={};for k,x in pairs(v) do out[k]=copy(x) end;return out end
local function identity(folder) return '\nOMO_EDITOR_IDENTITY:'..folder:gsub('.',function(c)return string.format('%02x',c:byte())end)..'\n' end
local function fixture()
  local f={store={},tasks={},forwards={},starts={},artifacts={},installs=0,ports=0,stops=0,now=1000,config={},host={os='linux',arch='amd64'}}
  local function hash(s) -- Distinct deterministic fixture keys, not an integrity implementation.
    local h=17; for i=1,#s do h=(h*31+s:byte(i))%1000000007 end
    return string.format('%064d',h)
  end
  f.omo={sha256_hex=hash, now=function() return f.now end, log=function() end,
    store_get=function(k) return copy(f.store[k]) end, store_set=function(k,v) local count=0;for _ in pairs(f.store) do count=count+1 end;if not f.store[k] and count>=1000 then return nil,'the plugin stores at most 1000 keys' end;f.store[k]=copy(v); return true end,
    store_delete=function(k) f.store[k]=nil; return true end,
    store_keys=function() local out={};for k in pairs(f.store) do out[#out+1]=k end; return out end,
    data_dir=function() return '/plugin data' end,
    task_temp_dir=function() f.temp_sequence=(f.temp_sequence or 0)+1;return {id='temp-'..f.temp_sequence,path='/tmp/omo-'..f.temp_sequence} end,
    exec=function(cmd,script,mode,folder) same(mode,'identity'); f.identity=folder; return f.identity_output or identity(folder),'' end,
    artifact_start=function(a) assert(#a.key<=64,'artifact key exceeds 64 bytes'); if a.key:find(f.config.editor or 'code-server',1,true) then return nil,'artifact key contains a configuration value' end; f.installs=f.installs+1; f.artifact_options=a; local old=f.artifacts[a.key]
      if old and old.state=='failed' and not a.retry then return nil,'explicit retry required' end
      f.artifacts[a.key]={state='running',phase='checking',bytes=0,total_bytes=0,verification='sha256'};return f.artifacts[a.key] end,
    artifact_status=function(k) return f.artifacts[k] end,
    task_list=function() return copy(f.tasks) end,
    task_start=function(a) f.starts[#f.starts+1]=a;local t={id='task-'..#f.starts,name=a.name,ticket=a.ticket,state='running',output={}}
      f.tasks[#f.tasks+1]=t;return t end,
    task_stop=function(name) f.stops=f.stops+1;for _,t in ipairs(f.tasks) do if t.name==name then t.state='exited';for i=#f.forwards,1,-1 do if f.forwards[i].task_id==t.id then table.remove(f.forwards,i) end end end end;return true end,
    free_loopback_port=function() f.ports=f.ports+1;return 40000+f.ports end,
    forward_add=function(a) f.forward_options=a;f.forward_sequence=(f.forward_sequence or 0)+1;local id=string.format('%032d',f.forward_sequence);local x={id=id,port=a.port,ticket=a.ticket,base_path='/proxy/'..id..'/',address='http://board/proxy/'..id..'/',activity={active_websockets=0,no_client_since='now'}};f.forwards[#f.forwards+1]=x;return x end,
    forward_list=function() return copy(f.forwards) end,
    forward_attach=function(id,task) for _,x in ipairs(f.forwards) do if x.id==id then x.task_id=task end end; return true end,
    forward_remove=function(id) for i=#f.forwards,1,-1 do if f.forwards[i].id==id then table.remove(f.forwards,i);return true end end;return nil,'forward not found' end,
    forward_idle_watch=function(id,seconds) f.watch={id=id,seconds=seconds};return true end}
  f.ticket={number=7,folders={{project='alpha',path='/work/-- quoted " 世界',available=true,kind='worktree'},{project='beta',path='/work/second',available=true,kind='worktree'}}}
  function f:run(action,project,ticket)
    local selected_ticket=self.ticket;if ticket==false then selected_ticket=nil end
    local env={omo=self.omo,config=self.config,event={event='manual',host=self.host,data={action=action,ticket=selected_ticket,project={id=project or 'alpha',kind=ticket==false and 'repository' or 'worktree',path='/repo/'..(project or 'alpha'),available=true}}}}
    setmetatable(env,{__index=_G}); local fn=assert(loadstring(source));setfenv(fn,env);return fn()
  end
  function f:ready() self.artifacts[self.artifact_options.key]={state='ready',phase='ready',path='/verified cache',verification='sha256'} end
  function f:timer(kind) local env={omo=self.omo,config=self.config,event={event=kind or 'timer',data={},host=self.host}};setmetatable(env,{__index=_G});local fn=assert(loadstring(source));setfenv(fn,env);return fn() end
  function f:opened(action,project,ticket) self:run(action or 'open-ticket',project,ticket);self:ready();self:run((action=='open-project') and 'status-project' or 'status-ticket',project,ticket);local t=self.tasks[#self.tasks];t.output={'OMO_EDITOR_READY'};return self:run((action=='open-project') and 'status-project' or 'status-ticket',project,ticket) end
  return f
end
local function test(name,fn) if arg[1] and not name:find(arg[1],1,true) then return end;local ok,err=pcall(fn);if not ok then error(name..': '..err) end;passed=passed+1;print('ok '..name) end

test('default pin verifies before launch and pending completes',function()
 local f=fixture();local r=f:run('open-ticket');contains(r.message,'VS Code pending:');same(#f.starts,0)
 same(f.artifact_options.sha256,'2f6db6c3e8b733dd6b3aeee83532e148b2bae2e392353ba85452d03572348962')
 contains(f.artifact_options.url,'/v4.145.2/');same(f.artifact_options.strip_components,1)
 f:ready();r=f:run('status-ticket');same(#f.starts,1);same(f.starts[1].ticket,7);contains(f.starts[1].command,'/lib/node')
 same(f.identity,'/work/-- quoted " 世界');same(f.forward_options.host,'127.0.0.1');same(f.forward_options.strip_prefix,true)
 same(f.starts[1].temp_dir,'temp-1');same(f.starts[1].args[10],'/tmp/omo-1')
 f.tasks[1].output={'OMO_EDITOR_READY'};r=f:run('status-ticket');same(r.forward_id,f.forwards[1].id);same(f.watch.seconds,1800);same(r.url,nil)
end)

test('alternate editor platform matrix and no Microsoft consent bypass',function()
 local f=fixture();f.config.editor='openvscode';f:run('open-ticket');same(f.artifact_options.sha256,'b433bf4f0227321a7014d8460d10a8f958adc0f45aa79bd889e84e65e8f88363');f:ready();f:run('status-ticket');same(f.forward_options.strip_prefix,false);same(f.starts[1].args[3],'openvscode')
 for _,osname in ipairs({'darwin','windows'}) do f=fixture();f.host.os=osname;f.config.editor='openvscode';contains(f:run('open-ticket').message,'unavailable');same(f.installs,0) end
 f=fixture();f.host.os='windows';f.host.arch='arm64';contains(f:run('open-ticket').message,'unavailable');same(f.installs,0)
 for _,accepted in ipairs({false,true}) do f=fixture();f.config.editor='microsoft';f.config.accept_microsoft_license=accepted;contains(f:run('open-ticket').message,'terms have not been confirmed');same(f.installs,0);same(#f.starts,0) end
end)

test('all native artifact pins and runtime layouts',function()
 for _,c in ipairs({{'linux','arm64'},{'darwin','amd64'},{'darwin','arm64'},{'windows','amd64'}}) do local f=fixture();f.host.os=c[1];f.host.arch=c[2];f:run('open-ticket');same(#f.artifact_options.sha256,64);f:ready();f:run('status-ticket');contains(f.starts[1].command,c[1]=='windows' and 'node.exe' or '/lib/node') end
 local f=fixture();f.host.arch='arm64';f.config.editor='openvscode';f:run('open-ticket');same(f.artifact_options.sha256,'36d9c14036489b63de84ebace837fcacf7e60e669a0dc715802c5443684ea4dc')
end)

test('authoritative selected project and no folder fallback',function()
 local f=fixture();f:opened('open-ticket','beta');same(f.identity,'/work/second')
 f=fixture();f.ticket.folders[1].available=false;f.ticket.folders[1].path='';contains(f:run('open-ticket').message,'unavailable');same(f.installs,0)
 f=fixture();contains(f:run('open-ticket','missing').message,'unavailable');same(f.installs,0)
 f=fixture();f:opened('open-project','beta',false);same(f.identity,'/repo/beta');same(f.starts[1].ticket,nil)
end)

test('repeated pending clicks and healthy instances reuse task and forward',function()
 local f=fixture();f:run('open-ticket');f:run('open-ticket');same(f.installs,1);f:ready();f:run('status-ticket');f:run('status-ticket');same(#f.starts,1)
 f.tasks[1].output={'OMO_EDITOR_READY'};local r=f:run('status-ticket');local again=f:run('open-ticket');same(again.forward_id,r.forward_id);same(#f.starts,1);same(#f.forwards,1)
end)

test('stale forward stops its exact old run and recreates only on click',function()
 local f=fixture();f:opened();f.forwards={};local r=f:run('status-ticket');same(r.forward_id,nil);same(#f.starts,1)
 f:run('open-ticket');same(f.installs,2);same(#f.starts,1);f:ready();f:run('status-ticket');same(#f.starts,2);same(f.stops,1)
end)

test('early exit is actionable and retry is explicit',function()
 local f=fixture();f:run('open-ticket');f:ready();f:run('status-ticket');f.tasks[1].state='failed';f.tasks[1].output={'loader ABI failed'}
 local r=f:run('status-ticket');contains(r.message,'settings');same(r.forward_id,nil);same(#f.starts,1);same(#f.forwards,0)
end)

test('port collisions retry at most three times',function()
 local f=fixture();f:run('open-ticket');f:ready();f:run('status-ticket')
 for i=1,3 do f.tasks[#f.tasks].state='failed';f.tasks[#f.tasks].output={'OMO_EDITOR_PORT_COLLISION'};f:run('status-ticket') end
 same(#f.starts,3);same(#f.forwards,0)
end)

test('failed downloads require next explicit click',function()
 local f=fixture();f:run('open-ticket');f.artifacts[f.artifact_options.key]={state='failed',error='checksum mismatch'};local r=f:run('status-ticket');contains(r.message,'checksum');same(f.installs,1)
 f:run('open-ticket');same(f.artifact_options.retry,true)
end)

test('startup leaves inert state for manual pruning and never starts editors',function()
 local f=fixture();f.store['instance:a']={task='old'};f.store['request:a']={instance='instance:a'};f.store['other']='keep'
 local env={omo=f.omo,config={},event={event='omo_started',data={},host=f.host}};setmetatable(env,{__index=_G});local fn=assert(loadstring(source));setfenv(fn,env);fn()
 same(f.store['instance:a'].task,'old');same(f.store.other,'keep');same(#f.starts,0);same(f.installs,0)
 f:run('open-ticket');same(f.store['instance:a'],nil);same(f.store['request:a'],nil)
end)

test('storage failure after spawn cannot leak an unwatched or unrecorded editor',function()
 local f=fixture();f:run('open-ticket');f:ready()
 local original=f.omo.store_set
 f.omo.store_set=function(k,v) if #f.starts>0 then return nil,'storage failed' end;return original(k,v) end
 local ok,r=pcall(function() return f:run('status-ticket') end)
 same(#f.starts,1);same(f.tasks[1].state,'exited');same(#f.forwards,0)
end)


test('completed folders cannot fill plugin store and block new work',function()
 local f=fixture()
 for i=1,510 do
   f.ticket.number=i;f.ticket.folders[1].path='/work/folder-'..i
   f:opened();f.tasks={};f.forwards={}
 end
 local count=0;for _ in pairs(f.store) do count=count+1 end
 assert(count<20,'stale records retained: '..count)
end)

test('maintenance reaps orphan resources left by an interrupted hook',function()
 local f=fixture();f.tasks={{id='orphan',name='cs-project-orphan',state='running',output={}}};f.forwards={{id='orphan-forward',task_id='orphan'}}
 local env={omo=f.omo,config={},event={event='timer',data={},host=f.host}};setmetatable(env,{__index=_G});local fn=assert(loadstring(source));setfenv(fn,env);fn()
 same(f.tasks[1].state,'exited');same(#f.forwards,0);same(f.installs,0)
end)


test('distinct canonical identities differing only by case are not folded on Windows',function()
 local f=fixture();f.host.os='windows';f:opened('open-project','Repo',false)
 f:opened('open-project','repo',false);same(#f.starts,2);same(#f.forwards,2)
end)


test('maintenance preserves recent exit evidence for bounded collision retry',function()
 local f=fixture();f:run('open-ticket');f:ready();f:run('status-ticket');f.tasks[1].state='failed';f.tasks[1].output={'OMO_EDITOR_PORT_COLLISION'};f.forwards={}
 local env={omo=f.omo,config={},event={event='timer',data={},host=f.host}};setmetatable(env,{__index=_G});local fn=assert(loadstring(source));setfenv(fn,env);fn()
 f:run('status-ticket');same(#f.starts,2)
end)


test('unverified artifact and failed attachment cannot execute or leak',function()
 local f=fixture();f:run('open-ticket');f:ready();f.artifacts[f.artifact_options.key].verification='unverified'
 contains(f:run('status-ticket').message,'failed');same(#f.starts,0)
 f=fixture();f:run('open-ticket');f:ready();f.omo.forward_attach=function()return nil,'already exited' end
 contains(f:run('status-ticket').message,'attach');same(f.tasks[1].state,'exited');same(#f.forwards,0)
end)

test('subdomain forwarding uses root base with exact same loopback launch',function()
 local f=fixture();local add=f.omo.forward_add
 f.omo.forward_add=function(a)local v=add(a);v.address='http://'..v.id..'.apps.localhost/';return v end
 f:opened();same(f.starts[1].args[9],'/');same(f.forward_options.host,'127.0.0.1')
end)


test('global project action rejects ticket context rather than creating an unbound worktree editor',function()
 local f=fixture();contains(f:run('open-project').message,'ticket');same(f.installs,0);same(#f.starts,0)
end)

test('timer cannot reap a manual launch between resource creation and persistence',function()
 for _,boundary in ipairs({'forward_add','task_start','forward_attach','forward_idle_watch','store_set'}) do
  local f=fixture();f:run('open-ticket');f:ready();local original=f.omo[boundary]
  f.omo[boundary]=function(...) local a={...};local out={original(unpack(a))};if boundary~='store_set' or tostring(a[1]):sub(1,9)=='instance:' then f:timer() end;return unpack(out) end
  local r=f:run('status-ticket');contains(r.message,'pending');same(#f.starts,1);same(f.tasks[1].state,'running');same(#f.forwards,1);same(f.stops,0)
  f:timer('omo_started');same(f.tasks[1].state,'running');same(#f.forwards,1)
  f.tasks[1].output={'OMO_EDITOR_READY'};same(f:run('status-ticket').forward_id,f.forwards[1].id)
 end
end)

test('timer resumes orphan cleanup after an interrupted manual hook grace',function()
 local f=fixture();f:run('open-ticket');f.tasks={{id='orphan',name='orphan',state='running',output={}}};f.forwards={{id='orphan-forward',task_id='orphan'}}
 f:timer();same(f.tasks[1].state,'running');f.now=f.now+121;f:timer();same(f.tasks[1].state,'exited');same(#f.forwards,0)
end)

test('cleanup tolerates task exit callback and concurrent forward removal',function()
 local f=fixture();f:opened();f.now=f.now+100;f.tasks[1].output={};for _,v in pairs(f.store) do if type(v)=='table' and v.key then v.ready=false end end
 contains(f:run('status-ticket').message,'timed out');same(#f.forwards,0);same(f.tasks[1].state,'exited')
 f=fixture();f:opened();f.tasks[1].state='failed';local original=f.omo.forward_remove
 f.omo.forward_remove=function(id) original(id);return nil,'forward not found' end
 contains(f:run('status-ticket').message,'stopped');same(#f.forwards,0)
end)

test('successive runs never reuse a task name that can stop another stable identity',function()
 local f=fixture();f:opened();local old=f.starts[1].name;f.tasks[1].state='exited';f.forwards={};f:opened();assert(old~=f.starts[2].name,'task name reused')
end)

test('identity marker ignores merged runtime diagnostics and preserves canonical alias reuse',function()
 local f=fixture();f.identity_output='preload stdout\nwarning on stderr\n'..identity('/canonical/quote "\n世界')..'trailing warning\n';f:opened()
 local first=f.starts[1].name;f.ticket.folders[1].path='/work/alias';f.identity_output='different diagnostics\n'..identity('/canonical/quote "\n世界');f:opened();same(#f.starts,1);same(f.starts[1].name,first)
end)

test('missing malformed or ambiguous identity marker cannot create resources',function()
 for _,output in ipairs({'/raw/path','warning OMO_EDITOR_IDENTITY:6162\n','\nOMO_EDITOR_IDENTITY:abc\n','\nOMO_EDITOR_IDENTITY:00\n',identity('/one')..identity('/two')}) do
  local f=fixture();f.identity_output=output;f:run('open-ticket');f:ready();contains(f:run('status-ticket').message,'resolve');same(#f.starts,0);same(#f.forwards,0)
 end
end)

test('workspace or error text containing readiness and collision words is not a sentinel',function()
 local f=fixture();f:run('open-ticket');f:ready();f:run('status-ticket');f.tasks[1].output={'Workspace: "/repo/OMO_EDITOR_READY"'}
 local r=f:run('status-ticket');contains(r.message,'pending');same(r.forward_id,nil)
 f.tasks[1].state='failed';f.tasks[1].output={'Failed: /repo/OMO_EDITOR_PORT_COLLISION'}
 contains(f:run('status-ticket').message,'stopped');same(#f.starts,1)
end)

test('failed attachment or idle watch requires a fresh integrity check on next click',function()
 for _,boundary in ipairs({'forward_attach','forward_idle_watch','task_start','task_temp_dir','forward_add','free_loopback_port'}) do
  local f=fixture();f:run('open-ticket');f:ready();f.omo[boundary]=function() return nil,'fixture refusal' end
  f:run('status-ticket');same(#f.forwards,0)
  for _,t in ipairs(f.tasks) do assert(t.state~='running') end
  f:run('open-ticket');same(f.installs,2)
 end
end)

test('a manual replacement after timer lease check survives the older resource snapshot',function()
 local f=fixture();f:opened();f.now=f.now+121
 for k in pairs(f.store) do if k:sub(1,9)=='instance:' then f.store[k]=nil end end
 local keys=f.omo.store_keys;local interleave=true
 f.omo.store_keys=function()
  if interleave then interleave=false;f:opened() end
  return keys()
 end
 f:timer();same(#f.starts,2);same(f.tasks[2].state,'running');same(#f.forwards,1);same(f.forwards[1].task_id,f.tasks[2].id)
end)

test('a refused ticket binding retains the owner and next explicit click reverifies',function()
 local f=fixture();f.identity_output=identity('/canonical/shared');f:opened()
 f:run('open-project','alpha',false);f:ready();contains(f:run('status-project','alpha',false).message,'another ticket binding')
 same(#f.starts,1);same(f.tasks[1].state,'running');same(f.tasks[1].ticket,7)
 f:run('open-project','alpha',false);same(f.installs,3)
end)


-- Removing canonical ownership checks must fail these tests by returning another
-- forward, spawning another editor, or stopping the existing unsaved session.
local function assert_owner_preserved(f,owner)
 same(#f.starts,1);same(#f.forwards,1);same(f.forwards[1].id,owner.forward_id)
 same(f.tasks[1].state,'running');same(f.stops,0);same(f.ports,1);same(f.temp_sequence,1)
end
local function assert_stop_required(r)
 same(r.forward_id,nil);contains(r.message,'Stop');contains(r.message,'plugin settings')
end
for _,editors in ipairs({{'code-server','openvscode'},{'openvscode','code-server'}}) do
 test('selection switch refuses '..editors[1]..' to '..editors[2]..' until Stop',function()
  for _,is_ready in ipairs({false,true}) do
   local f=fixture();f.config.editor=editors[1];local owner=f:opened()
   if not is_ready then
    f.tasks[1].output={}
    for _,rec in pairs(f.store) do if type(rec)=='table' and rec.key then rec.ready=false end end
   end
   f.config.editor=editors[2]
   assert_stop_required(f:run('status-ticket'));assert_stop_required(f:run('open-ticket'))
   assert_owner_preserved(f,owner);same(f.installs,1)
   f.config.editor=editors[1];local r=f:run('open-ticket')
   if is_ready then same(r.forward_id,owner.forward_id) else contains(r.message,'pending') end
   f.config.editor=editors[2];f.omo.task_stop(f.tasks[1].name)
   f:run('open-ticket');same(f.installs,2);f:ready();f:run('status-ticket')
   same(#f.starts,2);same(f.starts[2].args[3],editors[2]);same(#f.forwards,1);same(f.temp_sequence,2);same(f.ports,2)
  end
 end)
 test('canonical alias refuses '..editors[1]..' to '..editors[2]..' without duplicate resources',function()
  local f=fixture();f.config.editor=editors[1];f.identity_output=identity('/canonical/shared');local owner=f:opened()
  f.config.editor=editors[2];f.ticket.folders[1].path='/alias/shared'
  f:run('open-ticket');f:ready();assert_stop_required(f:run('status-ticket'))
  assert_owner_preserved(f,owner)
  f.now=f.now+121;f:timer();assert_owner_preserved(f,owner)
  f.omo.task_stop(f.tasks[1].name);local r=f:opened()
  same(#f.starts,2);same(f.starts[2].args[3],editors[2]);same(r.forward_id,f.forwards[1].id);same(f.temp_sequence,2)
 end)
end

test('canonical folder excludes another pinned version and preserves its live owner',function()
 local f=fixture();f.identity_output=identity('/canonical/shared');local owner=f:opened()
 local old_key='instance:'..f.omo.sha256_hex('/canonical/shared\0code-server\0previous-pinned-version')
 for key,rec in pairs(copy(f.store)) do
  if type(rec)=='table' and rec.key then
   f.store[key]=nil;rec.key=old_key;rec.version='previous-pinned-version';f.store[old_key]=rec
  elseif type(rec)=='table' and rec.instance then
   rec.instance=old_key;rec.version='previous-pinned-version';f.store[key]=rec
  end
 end
 assert_stop_required(f:run('status-ticket'));assert_stop_required(f:run('open-ticket'));assert_owner_preserved(f,owner)
 f.ticket.folders[1].path='/alias/shared';f:run('open-ticket');f:ready()
 assert_stop_required(f:run('status-ticket'));assert_owner_preserved(f,owner)
end)

test('canonical alias binding checks cover both editors and ticket global ownership',function()
 for _,editor in ipairs({'code-server','openvscode'}) do
  for _,bindings in ipairs({{7,8},{7,0},{0,7}}) do
   for _,switch in ipairs({false,true}) do
    local f=fixture();f.config.editor=editor;f.identity_output=identity('/canonical/shared')
    local owner=bindings[1]==0 and f:opened('open-project','alpha',false) or f:opened()
    f.ticket.number=bindings[2];f.ticket.folders[1].path='/alias/shared'
    if switch then f.config.editor=editor=='code-server' and 'openvscode' or 'code-server' end
    local open=bindings[2]==0 and 'open-project' or 'open-ticket'
    local status=bindings[2]==0 and 'status-project' or 'status-ticket'
    local ticket=bindings[2]~=0
    f:run(open,'alpha',ticket);f:ready();assert_stop_required(f:run(status,'alpha',ticket))
    assert_owner_preserved(f,owner);same(f.tasks[1].ticket,bindings[1]~=0 and bindings[1] or nil)
   end
  end
 end
end)

test('canonical alias reuses matching editor version and binding while starting or ready',function()
 for _,editor in ipairs({'code-server','openvscode'}) do
  for _,bound in ipairs({false,true}) do
   for _,is_ready in ipairs({false,true}) do
    local f=fixture();f.config.editor=editor;f.identity_output=identity('/canonical/shared')
    local action=bound and 'open-ticket' or 'open-project';local status=bound and 'status-ticket' or 'status-project'
    local owner=f:opened(action,'alpha',bound)
    if not is_ready then f.tasks[1].output={};for _,rec in pairs(f.store) do if type(rec)=='table' and rec.key then rec.ready=false end end end
    if bound then f.ticket.folders[1].path='/alias/shared' end
    f:run(action,bound and 'alpha' or 'alias',bound);f:ready();local r=f:run(status,bound and 'alpha' or 'alias',bound)
    if is_ready then same(r.forward_id,owner.forward_id) else contains(r.message,'pending') end
    assert_owner_preserved(f,owner)
   end
  end
 end
end)

test('legacy tuple records also refuse another editor through a canonical alias',function()
 for _,editor in ipairs({'code-server','openvscode'}) do
  local f=fixture();f.config.editor=editor;f.identity_output=identity('/canonical/shared');local owner=f:opened()
  for _,rec in pairs(f.store) do if type(rec)=='table' and rec.key then rec.canonical=nil;rec.editor=nil;rec.version=nil end end
  f.config.editor=editor=='code-server' and 'openvscode' or 'code-server';f.ticket.folders[1].path='/alias/shared'
  f:run('open-ticket');f:ready();assert_stop_required(f:run('status-ticket'));assert_owner_preserved(f,owner)
 end
end)

print(passed..' tests passed')
