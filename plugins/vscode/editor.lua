-- Manual hooks exclude each other, but queued timer/startup hooks can overlap.
-- No request supplies a path.
local settings, data = config or {}, event.data or {}
local function message(s) return {message=s} end
local function pending(s) return message('VS Code pending: '..s) end
local function save(k,v) local ok,why=omo.store_set(k,v); if not ok then error(why) end end
local function drop(k) local ok,why=omo.store_delete(k); if not ok then error(why) end end

local function remove_forward(id)
  for _,f in ipairs(assert(omo.forward_list())) do
    if f.id==id then
      local ok,why=omo.forward_remove(id)
      if ok then return end
      -- Exit callbacks and a concurrent timer may have won the removal.
      for _,remaining in ipairs(assert(omo.forward_list())) do
        if remaining.id==id then error(why) end
      end
      return
    end
  end
end
local function stop_task(t)
  local ok,why=omo.task_stop(t.name)
  if ok then return end
  for _,remaining in ipairs(assert(omo.task_list())) do
    if remaining.id==t.id and remaining.state=='running' then error(why) end
  end
end

if event.event=='omo_started' then return end -- Old records are inert; never restart.
local manual=event.event=='manual'
-- These hooks declare 30-second deadlines. Allow grace for an interrupted hook;
-- the timer takes resource snapshots BEFORE checking this lease. Resources
-- created by a later manual hook cannot appear in those snapshots.
if manual then save('manual-until',omo.now()+120) end
if event.event=='timer' or data.action=='open-ticket' or data.action=='open-project' then
  local tasks,forwards=assert(omo.task_list()),assert(omo.forward_list())
  if event.event=='timer' and (omo.store_get('manual-until') or 0)>omo.now() then return end
  local task_by_id,forward_by_id,kept_tasks,kept_forwards={},{},{},{}
  for _,t in ipairs(tasks) do task_by_id[t.id]=t end
  for _,f in ipairs(forwards) do forward_by_id[f.id]=f end
  local keys=assert(omo.store_keys())
  for _,key in ipairs(keys) do
    if key:sub(1,9)=='instance:' then
      local rec=omo.store_get(key)
      local t=rec and task_by_id[rec.task]
      local f=rec and forward_by_id[rec.forward]
      if t and t.state=='running' and f and f.task_id==t.id then
        kept_tasks[t.id]=true;kept_forwards[f.id]=true
      elseif manual then drop(key) end
    end
  end
  for _,key in ipairs(keys) do
    if manual and key:sub(1,8)=='request:' then
      local req=omo.store_get(key)
      if req and ((req.instance and not omo.store_get(req.instance)) or
        (not req.instance and omo.now()-(req.requested or 0)>35*60)) then drop(key) end
    end
  end
  for _,t in ipairs(tasks) do if t.state=='running' and not kept_tasks[t.id] then stop_task(t) end end
  -- Only the original snapshot: a fresh list could contain a concurrent launch.
  -- Unique task names ensure an old stop can never stop a replacement run.
  for _,f in ipairs(forwards) do if not kept_forwards[f.id] then remove_forward(f.id) end end
  if event.event=='timer' then return end
end

local editor=settings.editor or 'code-server'
if editor=='microsoft' then
  return message("Microsoft's VS Code for the web is unavailable: Microsoft's terms have not been confirmed for this integration. See https://code.visualstudio.com/license/server and https://code.visualstudio.com/docs/remote/faq. A saved acceptance value cannot enable it.")
end
if editor~='code-server' and editor~='openvscode' then return message('Unknown editor. Select an editor in plugin settings.') end
local host=event.host
local platform=host.os..'-'..host.arch
local checksums={
  ['code-server']={
    ['linux-amd64']='2f6db6c3e8b733dd6b3aeee83532e148b2bae2e392353ba85452d03572348962',
    ['linux-arm64']='e867a34416e2da4a83496b09580001893e7a4e5bb58a0624c4fcd22819e7d53d',
    ['darwin-amd64']='5fb7ac2bd60538400244d942f649559e0ff4fb3d8792979287f504780f357af6',
    ['darwin-arm64']='d6699692f1e59d918517c81f9d2a4ef335132128cd45725b22236d5fe09636d4',
    ['windows-amd64']='d88f8a00831334da291ad29b35714c7bf6d3dc398e5b6f5ed1b20034c1da7675',
  },
  openvscode={
    ['linux-amd64']='b433bf4f0227321a7014d8460d10a8f958adc0f45aa79bd889e84e65e8f88363',
    ['linux-arm64']='36d9c14036489b63de84ebace837fcacf7e60e669a0dc715802c5443684ea4dc',
  },
}
if not checksums[editor][platform] then
  return message('This editor is unavailable on '..platform..': no pinned native publisher build. OpenVSCode supports Linux amd64/arm64; code-server also supports macOS amd64/arm64 and Windows amd64.')
end
local idle=tonumber(settings.idle_minutes or 30)
if not idle or idle~=idle or idle<1 or idle>10080 then return message('Set idle_minutes between 1 and 10080 in plugin settings.') end
local ticket,project=data.ticket,data.project
local ticket_action=data.action=='open-ticket' or data.action=='status-ticket'
local opening=data.action=='open-ticket' or data.action=='open-project'
local folder,binding
if ticket_action then
  if not ticket then return message('The ticket folder is unavailable.') end
  if ticket.column=='done' then return message('Work on this ticket is finished; its editor cannot be started again.') end
  for _,f in ipairs(ticket.folders or {}) do
    if (project and f.project==project.id) or (not project and #(ticket.folders or {})==1) then
      if f.available and f.path~='' then folder=f.path;binding=ticket.number end
    end
  end
elseif ticket then return message('Use the ticket action for a ticket folder; a project action requires repository context without a ticket.')
elseif project and project.available and project.path~='' then folder=project.path end
if not folder then
  if ticket_action and #(ticket.projects or {})==0 and not project then
    return message('The ticket working folder is unavailable. It may not have been created, may have been removed, or may no longer match its recorded directory.')
  end
  if ticket_action then return message('The selected ticket worktree is unavailable. It may not have been created, may have been removed, or may no longer match its recorded worktree.') end
  return message('The selected project repository folder is unavailable.')
end
local versions={['code-server']='4.145.2',openvscode='1.109.5'}
local version=versions[editor]
local artifact_key=omo.sha256_hex(editor..'\0'..version..'\0'..platform)
local request_key='request:'..omo.sha256_hex(tostring(binding or 0)..'\0'..folder)
local req=omo.store_get(request_key)
local function records()
  return assert(omo.task_list()),assert(omo.forward_list())
end
local function find_task(tasks,id)
  for _,t in ipairs(tasks) do if t.id==id then return t end end
end
local function find_forward(forwards,id)
  for _,f in ipairs(forwards) do if f.id==id then return f end end
end
local function cleanup(rec,tasks,forwards)
  local t=find_task(tasks,rec.task)
  if t and t.state=='running' then stop_task(t) end
  remove_forward(rec.forward)
  drop(rec.key)
end
local function output_has(t,text)
  for _,line in ipairs(t.output or {}) do if line==text then return true end end
  return false
end
local function ready(rec,t,forward)
  if not rec.ready and not output_has(t,'OMO_EDITOR_READY') then return false end
  local ok,why=omo.forward_idle_watch(forward.id,idle*60)
  if not ok then return nil,why end
  rec.ready=true;save(rec.key,rec)
  return {message='VS Code is ready. Edits go directly into this folder.',forward_id=forward.id}
end
local function stop_required()
  return message('This folder already has a running editor with another ticket binding or editor/version. Stop that editor in plugin settings before reopening here.')
end
local function compatible(rec,t)
  return (t.ticket or 0)==(binding or 0) and
    (not rec.editor or rec.editor==editor) and (not rec.version or rec.version==version)
end
if req and (req.editor~=editor or req.version~=version) then
  local rec=req.instance and omo.store_get(req.instance)
  local tasks,forwards=records()
  local t=rec and find_task(tasks,rec.task)
  local f=rec and find_forward(forwards,rec.forward)
  -- Keep the owning request intact: switching back can still reuse unsaved work.
  if t and t.state=='running' and f and f.task_id==t.id then return stop_required() end
  if not opening then return message('The editor selection changed. Click Open in VS Code again.') end
  req=nil
end
-- Reuse only stable identities still owned by this runtime. No stale restart.
if req and req.instance then
  local rec=omo.store_get(req.instance)
  local tasks,forwards=records()
  local t=rec and find_task(tasks,rec.task)
  local f=rec and find_forward(forwards,rec.forward)
  if rec and t and t.state=='running' and f and f.task_id==t.id then
    if not compatible(rec,t) then return stop_required() end
    local result,why=ready(rec,t,f)
    if result then return result end
    if why then cleanup(rec,tasks,forwards);return message('Cannot arm editor idle cleanup: '..why) end
    if omo.now()-rec.started<90 then return pending('starting the editor; output is available in plugin settings') end
    cleanup(rec,tasks,forwards)
    return message('Editor readiness timed out. Inspect its output in plugin settings and click Open to retry.')
  end
  local collision_retry=false
  if rec then
    local collision=t and output_has(t,'OMO_EDITOR_PORT_COLLISION') and not rec.ready
    cleanup(rec,tasks,forwards)
    if collision and rec.attempt<3 then
      req.attempt=rec.attempt+1;collision_retry=true
    else
      -- A fresh execution requires a fresh artifact_start integrity check,
      -- including after Stop, idle, forward removal or an early exit.
      drop(request_key);req=nil
      if collision then return message('No loopback port could be bound after three attempts. See output in plugin settings and retry explicitly.') end
      if not opening then return message('Editor stopped or its forward disappeared. Inspect its output in plugin settings; click Open to create a new instance.') end
    end
  else
    drop(request_key);req=nil
    if not opening then return message('Editor state expired. Click Open in VS Code to create it again.') end
  end
  if collision_retry then req.instance=nil;save(request_key,req) end
end
if not req then
  if not opening then return message('No editor request is pending. Click Open in VS Code.') end
  req={editor=editor,version=version,attempt=1,requested=omo.now()};save(request_key,req)
end

-- A click initiates one full-tree integrity check/install; status never restarts it.
if not req.installing then
  if not opening then return message('No installation is pending. Click Open in VS Code to retry.') end
  local suffix=platform:gsub('darwin','macos')
  local url
  if editor=='code-server' then url='https://github.com/coder/code-server/releases/download/v'..version..'/code-server-'..version..'-'..suffix..'.tar.gz'
  else
    suffix=platform:gsub('amd64','x64')
    url='https://github.com/gitpod-io/openvscode-server/releases/download/openvscode-server-v'..version..'/openvscode-server-v'..version..'-'..suffix..'.tar.gz'
  end
  local job,why=omo.artifact_start{key=artifact_key,url=url,sha256=checksums[editor][platform],format='tar.gz',strip_components=1,retry=true}
  if not job then drop(request_key);return message('Editor installation could not start: '..tostring(why)) end
  req.installing=true;save(request_key,req)
  return pending('checking the verified editor cache / preparing download')
end
local job,why=omo.artifact_status(artifact_key)
if not job then drop(request_key);return message('Editor installation state expired: '..tostring(why)..'. Click Open to retry.') end
if job.state=='running' then return pending(job.phase..' ('..tostring(job.bytes or 0)..' / '..tostring(job.total_bytes or 0)..' bytes)') end
if job.state~='ready' or job.verification~='sha256' or not job.path then
  drop(request_key);return message('Editor installation failed: '..tostring(job.error or 'verified artifact unavailable')..'. Check network, disk space and plugin log; click Open to retry.')
end
local node=job.path..(editor=='code-server' and '/lib/node' or '/node')..(host.os=='windows' and '.exe' or '')
-- Bundled verified runtime resolves aliases/case before deriving instance identity.
local output,err=omo.exec(node,'launch.cjs','identity',folder)
local canonical,count=nil,0
-- exec merges stdout/stderr and Node may run an inherited preload or warn
-- before launch.cjs starts. Only one complete, hex-encoded marker is identity.
for line in ((output or '')..'\n'):gmatch('([^\n]*)\n') do
  local encoded=line:match('^OMO_EDITOR_IDENTITY:([a-f0-9]+)\r?$')
  if encoded then
    count=count+1
    if #encoded%2==0 then canonical=encoded:gsub('..',function(pair)return string.char(tonumber(pair,16))end) end
  end
end
if err~='' or count~=1 or not canonical or canonical=='' or canonical:find('\0',1,true) then drop(request_key);return message('Editor runtime cannot resolve the folder. Check host ABI/runtime requirements and output in plugin settings: '..tostring(err)) end
if host.os=='windows' then canonical=canonical:gsub('\\','/') end
local function instance_key(name,release)
  return 'instance:'..omo.sha256_hex(canonical..'\0'..name..'\0'..release)
end
local key=instance_key(editor,version)
local tasks,forwards=records()
-- The tuple identifies reusable state, but the canonical folder owns exclusion
-- across editors, releases and ticket/global bindings. Recognize records from
-- before canonical metadata was stored using the shipped tuple identities.
local known={}
for name,release in pairs(versions) do known[instance_key(name,release)]=true end
local owner
for _,stored_key in ipairs(assert(omo.store_keys())) do
  if stored_key:sub(1,9)=='instance:' then
    local rec=omo.store_get(stored_key)
    if rec and (rec.canonical==canonical or known[stored_key]) then
      local t,f=find_task(tasks,rec.task),find_forward(forwards,rec.forward)
      if t and t.state=='running' and f and f.task_id==t.id then
        if stored_key~=key or not compatible(rec,t) then drop(request_key);return stop_required() end
        owner={rec=rec,task=t,forward=f}
      else cleanup(rec,tasks,forwards) end
    end
  end
end
if owner then
  local old,t,f=owner.rec,owner.task,owner.forward
  old.canonical=canonical;old.editor=editor;old.version=version;save(old.key,old)
  req.instance=key;save(request_key,req)
  local result,why=ready(old,t,f)
  if result then return result end
  if why then return message('Cannot arm editor idle cleanup: '..why) end
  return pending('starting the existing editor')
end
local port,port_error=omo.free_loopback_port()
if not port then drop(request_key);return message('No loopback port available: '..tostring(port_error)) end
local sequence=(omo.store_get('run-sequence') or 0)+1
save('run-sequence',sequence)
local forward,forward_error=omo.forward_add{name='VS Code '..(binding and 'ticket '..binding or 'project'),host='127.0.0.1',port=port,ticket=binding,strip_prefix=editor=='code-server'}
if not forward then drop(request_key);return message('Editor forward could not be registered: '..tostring(forward_error)) end
local base=forward.base_path
-- The address is a routing hint only. The final UI obtains a fresh Open grant.
if not forward.address:find('/proxy/'..forward.id..'/',1,true) then base='/' end
local id=key:sub(10,41)
local temporary,temp_error=omo.task_temp_dir()
if not temporary then remove_forward(forward.id);drop(request_key);return message('Editor runtime directory could not be allocated: '..tostring(temp_error)) end
local task,start_error=omo.task_start{
  name=(editor=='code-server' and 'cs-' or 'ovs-')..(binding and 't'..binding..'-' or 'project-')..id:sub(1,24)..'-'..string.format('%.0f',sequence),
  command=node,args={'launch.cjs','run',editor,job.path,folder,assert(omo.data_dir()),id,tostring(port),base,temporary.path},
  env={NODE_OPTIONS='',NODE_PATH=''},ticket=binding,temp_dir=temporary.id,
}
if not task then remove_forward(forward.id);drop(request_key);return message('Editor process could not start: '..tostring(start_error)..'. Check runtime requirements in the plugin README.') end
local attached,attach_error=omo.forward_attach(forward.id,task.id)
if not attached then
  stop_task(task);remove_forward(forward.id)
  drop(request_key)
  return message('Editor exited before its forward could attach: '..tostring(attach_error)..'. Inspect output in plugin settings.')
end
local rec={key=key,canonical=canonical,editor=editor,version=version,task=task.id,forward=forward.id,started=omo.now(),attempt=req.attempt,ready=false}
-- Watch immediately as well: an abandoned first-use tab cannot leak a task.
local watched,watch_error=omo.forward_idle_watch(forward.id,idle*60)
if not watched then cleanup(rec,assert(omo.task_list()),assert(omo.forward_list()));drop(request_key);return message('Cannot arm editor idle cleanup: '..tostring(watch_error)) end
local persisted,store_error=pcall(function()
  save(key,rec);req.instance=key;save(request_key,req)
end)
if not persisted then
  -- Reap first even when persistent storage itself is failing.
  omo.task_stop(task.name);omo.forward_remove(forward.id)
  omo.store_delete(key);omo.store_delete(request_key)
  return message('Editor state could not be saved; the new editor was stopped. Check disk/database health and retry explicitly.')
end
return pending('starting the editor; output is available in plugin settings')
