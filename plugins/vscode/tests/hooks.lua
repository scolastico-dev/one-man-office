-- Boundary fixtures: real hook code; slow artifact/process/registry adapters
-- follow the published generic contracts. No publisher downloads here.
local source = assert(io.open('plugins/vscode/editor.lua')):read('*a')
local passed = 0
local function same(a,b) assert(a==b, tostring(a)..' ~= '..tostring(b)) end
local function contains(s,t) assert(string.find(s,t,1,true), tostring(s)..' lacks '..t) end
local function fixture()
  local f={store={},tasks={},forwards={},starts={},artifacts={},installs=0,ports=0,stops=0,now=1000,config={},host={os='linux',arch='amd64'}}
  local function hash(s) -- Distinct deterministic fixture keys, not an integrity implementation.
    local h=17; for i=1,#s do h=(h*31+s:byte(i))%1000000007 end
    return string.format('%064d',h)
  end
  f.omo={sha256_hex=hash, now=function() return f.now end, log=function() end,
    store_get=function(k) return f.store[k] end, store_set=function(k,v) local count=0;for _ in pairs(f.store) do count=count+1 end;if not f.store[k] and count>=1000 then return nil,'the plugin stores at most 1000 keys' end;f.store[k]=v; return true end,
    store_delete=function(k) f.store[k]=nil; return true end,
    store_keys=function() local out={};for k in pairs(f.store) do out[#out+1]=k end; return out end,
    data_dir=function() return '/plugin data' end,
    exec=function(cmd,script,mode,folder) same(mode,'identity'); f.identity=folder; return folder,'' end,
    artifact_start=function(a) assert(#a.key<=64,'artifact key exceeds 64 bytes'); if a.key:find(f.config.editor or 'code-server',1,true) then return nil,'artifact key contains a configuration value' end; f.installs=f.installs+1; f.artifact_options=a; local old=f.artifacts[a.key]
      if old and old.state=='failed' and not a.retry then return nil,'explicit retry required' end
      f.artifacts[a.key]={state='running',phase='checking',bytes=0,total_bytes=0,verification='sha256'};return f.artifacts[a.key] end,
    artifact_status=function(k) return f.artifacts[k] end,
    task_list=function() return f.tasks end,
    task_start=function(a) f.starts[#f.starts+1]=a;local t={id='task-'..#f.starts,name=a.name,ticket=a.ticket,state='running',output={}}
      f.tasks[#f.tasks+1]=t;return t end,
    task_stop=function(name) f.stops=f.stops+1;for _,t in ipairs(f.tasks) do if t.name==name then t.state='exited' end end;return true end,
    free_loopback_port=function() f.ports=f.ports+1;return 40000+f.ports end,
    forward_add=function(a) f.forward_options=a;local id=string.format('%032d',#f.forwards+1);local x={id=id,port=a.port,ticket=a.ticket,base_path='/proxy/'..id..'/',address='http://board/proxy/'..id..'/',activity={active_websockets=0,no_client_since='now'}};f.forwards[#f.forwards+1]=x;return x end,
    forward_list=function() return f.forwards end,
    forward_attach=function(id,task) for _,x in ipairs(f.forwards) do if x.id==id then x.task_id=task end end; return true end,
    forward_remove=function(id) for i=#f.forwards,1,-1 do if f.forwards[i].id==id then table.remove(f.forwards,i) end end;return true end,
    forward_idle_watch=function(id,seconds) f.watch={id=id,seconds=seconds};return true end}
  f.ticket={number=7,folders={{project='alpha',path='/work/-- quoted " 世界',available=true,kind='worktree'},{project='beta',path='/work/second',available=true,kind='worktree'}}}
  function f:run(action,project,ticket)
    local selected_ticket=self.ticket;if ticket==false then selected_ticket=nil end
    local env={omo=self.omo,config=self.config,event={event='manual',host=self.host,data={action=action,ticket=selected_ticket,project={id=project or 'alpha',kind=ticket==false and 'repository' or 'worktree',path='/repo/'..(project or 'alpha'),available=true}}}}
    setmetatable(env,{__index=_G}); local fn=assert(loadstring(source));setfenv(fn,env);return fn()
  end
  function f:ready() self.artifacts[self.artifact_options.key]={state='ready',phase='ready',path='/verified cache',verification='sha256'} end
  function f:opened(action,project,ticket) self:run(action or 'open-ticket',project,ticket);self:ready();self:run((action=='open-project') and 'status-project' or 'status-ticket',project,ticket);local t=self.tasks[#self.tasks];t.output={'OMO_EDITOR_READY'};return self:run((action=='open-project') and 'status-project' or 'status-ticket',project,ticket) end
  return f
end
local function test(name,fn) local ok,err=pcall(fn);if not ok then error(name..': '..err) end;passed=passed+1;print('ok '..name) end

test('default pin verifies before launch and pending completes',function()
 local f=fixture();local r=f:run('open-ticket');contains(r.message,'VS Code pending:');same(#f.starts,0)
 same(f.artifact_options.sha256,'2f6db6c3e8b733dd6b3aeee83532e148b2bae2e392353ba85452d03572348962')
 contains(f.artifact_options.url,'/v4.145.2/');same(f.artifact_options.strip_components,1)
 f:ready();r=f:run('status-ticket');same(#f.starts,1);same(f.starts[1].ticket,7);contains(f.starts[1].command,'/lib/node')
 same(f.identity,'/work/-- quoted " 世界');same(f.forward_options.host,'127.0.0.1');same(f.forward_options.strip_prefix,true)
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

test('failed downloads require next explicit click and changed selection leaves existing alone',function()
 local f=fixture();f:run('open-ticket');f.artifacts[f.artifact_options.key]={state='failed',error='checksum mismatch'};local r=f:run('status-ticket');contains(r.message,'checksum');same(f.installs,1)
 f:run('open-ticket');same(f.artifact_options.retry,true)
 f=fixture();f:opened();f.config.editor='openvscode';f:run('open-ticket');same(#f.starts,1);same(f.stops,0);same(f.installs,2)
end)

test('startup clears only plugin instance references and never starts editors',function()
 local f=fixture();f.store['instance:a']={task='old'};f.store['request:a']={};f.store['other']='keep'
 local env={omo=f.omo,config={},event={event='omo_started',data={},host=f.host}};setmetatable(env,{__index=_G});local fn=assert(loadstring(source));setfenv(fn,env);fn()
 same(f.store['instance:a'],nil);same(f.store['request:a'],nil);same(f.store.other,'keep');same(#f.starts,0);same(f.installs,0)
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


test('Windows case-sensitive canonical folders stay distinct',function()
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

print(passed..' tests passed')
