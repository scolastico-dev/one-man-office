-- Pulls a ticket's base branch before work starts.
--
-- gate (the move named by config.mode): for every project of a story or of a
-- task without a story, find out whether the branch the ticket will be based
-- on (the branch checked out in the project's folder) is behind its upstream.
-- The upstream is fetched with git, with config.fetch_timeout_seconds, and
-- at most once per project and branch for one round of checks
-- (config.fetch_reuse_seconds, 60 by default, is how long a fetch counts).
--   up to date              go ahead, nothing shown
--   behind, can be pulled   ask (hold), or with config.automatic pull and go ahead
--   cannot be pulled        hold with the reason; never pulls: the branch
--                           diverged, uncommitted changes the pull would
--                           touch, no upstream, branch not checked out, or
--                           the remote cannot be reached
-- Only fast-forward pulls (git merge --ff-only). Nothing is merged, rebased,
-- stashed, reset, checked out or discarded.
--
-- manual (the choices of a hold): pull, continue, cancel.

local MODE_RELEASE, MODE_START = "release", "start"
local TOTAL_FETCH_SECONDS = 270 -- the gate's time limit is 5 minutes
local POLL_SECONDS = 0.1
local STORE_PRUNE_AFTER = 7 * 86400

local function number(key, default, low, high)
  local v = tonumber(config[key])
  if not v then
    return default
  end
  if v < low then
    return low
  end
  if v > high then
    return high
  end
  return v
end

local function plural(n, word)
  if n == 1 then
    return "1 " .. word
  end
  return tostring(n) .. " " .. word .. "s"
end

local function trim(s)
  return (string.gsub(s, "^%s+", ""):gsub("%s+$", ""))
end

local function first_line(s)
  local line = string.match(s, "[^\r\n]+")
  return trim(line or "")
end

-- git runs git in dir; it returns the output and whether git succeeded.
local function git(dir, ...)
  local out, err = omo.exec("git", "-C", dir, "-c", "gc.auto=0", "-c", "maintenance.auto=false", ...)
  return out, err == ""
end

-- split_z splits NUL separated text.
local function split_z(s)
  local list, from = {}, 1
  while from <= #s do
    local at = string.find(s, "\0", from, true)
    if not at then
      list[#list + 1] = string.sub(s, from)
      break
    end
    list[#list + 1] = string.sub(s, from, at - 1)
    from = at + 1
  end
  return list
end

local function now()
  return omo.now()
end

-- The store: "fetch:<hash>" is the result of the last fetch of a project's
-- branch, "held:<ticket>" what a hold needs to be carried out later.
local function prune()
  local keys = omo.store_keys()
  if not keys or #keys < 50 then
    return
  end
  for _, key in ipairs(keys) do
    local v = omo.store_get(key)
    if type(v) ~= "table" or type(v.at) ~= "number" or now() - v.at > STORE_PRUNE_AFTER then
      omo.store_delete(key)
    end
  end
end

-- upstream describes the upstream of the local branch, or nil.
local function upstream(dir, branch)
  local out, ok = git(dir, "for-each-ref", "--format=%(upstream)%09%(upstream:short)%09%(upstream:remotename)%09%(upstream:remoteref)", "refs/heads/" .. branch)
  if not ok then
    return nil
  end
  local full, short, remote, remoteref = string.match(trim(out), "^([^\t]+)\t([^\t]*)\t([^\t]*)\t([^\t]*)$")
  if not full or full == "" then
    return nil
  end
  return {full = full, short = short, remote = remote, remoteref = remoteref}
end

local spent = 0 -- seconds of fetching in this run

-- fetch fetches the upstream of branch once for a round of checks. It
-- returns true, or false and a short why.
local function fetch(dir, branch, up, limit)
  local key = "fetch:" .. omo.sha256_hex(dir .. "\0" .. branch)
  local cached = omo.store_get(key)
  local reuse = number("fetch_reuse_seconds", 60, 0, 86400)
  if type(cached) == "table" and type(cached.at) == "number" and now() - cached.at < reuse then
    return cached.ok == true, cached.why or ""
  end
  local ok, why = true, ""
  local budget = math.min(limit, TOTAL_FETCH_SECONDS - spent)
  if budget <= 0 then
    ok, why = false, "no time left"
  elseif up.remote == "" or up.remoteref == "" then
    ok, why = false, "the upstream has no remote"
  else
    local name = "fetch-" .. string.sub(omo.sha256_hex(key), 1, 16)
    local task, err = omo.task_start{
      name = name,
      command = "git",
      args = {"-C", dir, "-c", "gc.auto=0", "-c", "maintenance.auto=false", "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--", up.remote, "+" .. up.remoteref .. ":" .. up.full},
      env = {GIT_TERMINAL_PROMPT = "0"},
    }
    if not task then
      ok, why = false, tostring(err)
    else
      local waited = 0
      while true do
        local tasks = omo.task_list() or {}
        local record
        for _, t in ipairs(tasks) do
          if t.id == task.id then
            record = t
          end
        end
        if record and record.state == "exited" then
          if record.error and record.error ~= "" then
            ok, why = false, "git fetch failed"
          end
          break
        end
        if waited >= budget then
          omo.task_stop(name)
          ok, why = false, "timed out"
          break
        end
        omo.sleep(POLL_SECONDS)
        waited = waited + POLL_SECONDS
      end
      spent = spent + waited
    end
  end
  omo.store_set(key, {at = now(), ok = ok, why = why})
  return ok, why
end

-- dirty_conflict reports whether an uncommitted or untracked path of the
-- folder is one the pull would change.
local function dirty_conflict(dir, branch, up)
  local changed_out, ok = git(dir, "diff", "--name-only", "-z", "--no-renames", "refs/heads/" .. branch, up.full)
  if not ok then
    return true
  end
  local changed = {}
  for _, path in ipairs(split_z(changed_out)) do
    changed[path] = true
  end
  local status_out, status_ok = git(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
  if not status_ok then
    return true
  end
  local entries = split_z(status_out)
  local i = 1
  while i <= #entries do
    local entry = entries[i]
    local xy = string.sub(entry, 1, 2)
    if changed[string.sub(entry, 4)] then
      return true
    end
    if string.find(xy, "[RC]") then
      i = i + 1
      if entries[i] and changed[entries[i]] then
        return true
      end
    end
    i = i + 1
  end
  return false
end

-- check looks at one project. It returns {state = "current"}, {state =
-- "behind", n, line, ...} or {state = "blocked", reason}.
local function check(project, branch, dir, may_fetch, timeout)
  local label = "`" .. branch .. "` of " .. project
  local up = upstream(dir, branch)
  if not up then
    return {state = "blocked", reason = label .. " has no upstream branch to pull."}
  end
  if may_fetch and up.remote ~= "." then
    local ok = fetch(dir, branch, up, timeout)
    if not ok then
      return {state = "blocked", reason = "`" .. up.short .. "` of " .. project .. " could not be fetched, so it is unknown whether `" .. branch .. "` is behind it."}
    end
  end
  local counts, ok = git(dir, "rev-list", "--left-right", "--count", "refs/heads/" .. branch .. "..." .. up.full)
  local ahead, behind = string.match(trim(counts), "^(%d+)%s+(%d+)$")
  if not ok or not ahead then
    return {state = "blocked", reason = "`" .. up.short .. "` of " .. project .. " cannot be compared with `" .. branch .. "`."}
  end
  ahead, behind = tonumber(ahead), tonumber(behind)
  if behind == 0 then
    return {state = "current"}
  end
  if ahead > 0 then
    return {state = "blocked", reason = label .. " has diverged from `" .. up.short .. "` (" .. plural(ahead, "commit") .. " ahead, " .. tostring(behind) .. " behind) and cannot be fast-forwarded."}
  end
  local head = git(dir, "symbolic-ref", "-q", "HEAD")
  if trim(head) ~= "refs/heads/" .. branch then
    return {state = "blocked", reason = label .. " is not the branch checked out in the folder of " .. project .. ", so it cannot be pulled safely."}
  end
  if dirty_conflict(dir, branch, up) then
    return {state = "blocked", reason = "Uncommitted changes in " .. project .. " would be overwritten by pulling `" .. up.short .. "` into `" .. branch .. "`."}
  end
  return {state = "behind", n = behind, line = label .. " is " .. plural(behind, "commit") .. " behind `" .. up.short .. "`.", up = up, project = project, branch = branch, dir = dir}
end

-- pull fast-forwards the branch checked out in the folder.
local function pull(r)
  local out, ok = git(r.dir, "merge", "--ff-only", "--no-autostash", "--quiet", r.up.full)
  if ok then
    return true
  end
  return false, "`" .. r.branch .. "` of " .. r.project .. " could not be fast-forwarded: " .. first_line(out)
end

local function hold(reason, choices)
  return {action = "hold", reason = reason, choices = choices}
end

local CHOICE_PULL = {label = "Pull and continue", action = "pull"}
local CHOICE_CONTINUE = {label = "Continue without pulling", action = "continue"}
local CHOICE_CANCEL = {label = "Cancel", action = "cancel"}

local function gate()
  local d = event.data
  local mode = config.mode
  if mode ~= MODE_START then
    mode = MODE_RELEASE
  end
  if d.move ~= mode or d.in_story then
    return nil
  end
  local timeout = number("fetch_timeout_seconds", 30, 1, 240)
  local blocked, behind = {}, {}
  for _, p in ipairs(d.projects or {}) do
    local folder = p.folder or {}
    if folder.available and folder.path and p.base_branch and p.base_branch ~= "" then
      local r = check(p.project, p.base_branch, folder.path, true, timeout)
      if r.state == "blocked" then
        blocked[#blocked + 1] = r.reason
      elseif r.state == "behind" then
        behind[#behind + 1] = r
      end
    else
      omo.log("pull-base: " .. tostring(p.project) .. " is not checked: its folder or base branch is not known")
    end
  end
  if #blocked > 0 then
    return hold(table.concat(blocked, " "), {CHOICE_CONTINUE, CHOICE_CANCEL})
  end
  if #behind == 0 then
    return nil
  end
  if config.automatic == true then
    for _, r in ipairs(behind) do
      local ok, why = pull(r)
      if not ok then
        return hold(why, {CHOICE_CONTINUE, CHOICE_CANCEL})
      end
    end
    return nil
  end
  local lines, saved = {}, {}
  for _, r in ipairs(behind) do
    lines[#lines + 1] = r.line
    saved[#saved + 1] = {project = r.project, branch = r.branch, dir = r.dir}
  end
  prune()
  omo.store_set("held:" .. tostring(d.ticket.number), {at = now(), projects = saved})
  return hold(table.concat(lines, " ") .. " Pull before starting?", {CHOICE_PULL, CHOICE_CONTINUE, CHOICE_CANCEL})
end

local function manual()
  local action = event.data.action
  local number_ = event.data.ticket and event.data.ticket.number
  if not number_ then
    return "No ticket."
  end
  local key = "held:" .. tostring(number_)
  if action == "cancel" then
    omo.store_delete(key)
    local ok, err = omo.gate_cancel(number_)
    if not ok then
      return "The move could not be cancelled: " .. tostring(err)
    end
    return "Cancelled."
  end
  if action == "continue" then
    omo.store_delete(key)
    local ok, err = omo.gate_release(number_)
    if not ok then
      return "The move could not go on: " .. tostring(err)
    end
    return "Going on without pulling."
  end
  -- pull
  local held = omo.store_get(key)
  if type(held) ~= "table" or type(held.projects) ~= "table" then
    return "Nothing to pull: no question about this ticket is open."
  end
  local rs = {}
  for _, p in ipairs(held.projects) do
    local r = check(p.project, p.branch, p.dir, false, 0)
    if r.state == "blocked" then
      return r.reason .. " Nothing was pulled."
    elseif r.state == "behind" then
      rs[#rs + 1] = r
    end
  end
  for _, r in ipairs(rs) do
    local ok, why = pull(r)
    if not ok then
      return why
    end
  end
  omo.store_delete(key)
  local ok, err = omo.gate_release(number_)
  if not ok then
    return "Pulled, but the move could not go on: " .. tostring(err)
  end
  return "Pulled " .. plural(#rs, "project") .. "; going on."
end

if event.event == "gate" then
  return gate()
end
return manual()
