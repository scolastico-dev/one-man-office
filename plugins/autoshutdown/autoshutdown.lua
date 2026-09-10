-- Optional orderly shutdown for offices that have remained quiet.
-- All state is plugin-local so a restart can reconcile it against the office
-- session start supplied by the supervisor.

if event.event ~= "cron" then
  return
end

local data = event.data or {}
local now = tonumber(data.at_unix)
local office_started = tonumber(data.office_started_at_unix)
if not now or not office_started then
  return
end

local settings = config or {}
local idle_after_text = settings.idle_after or "30m"
local idle_after = omo.duration(idle_after_text)
if idle_after <= 0 then
  return
end

local session_key = "office_started_at_unix"
local previous_session = tonumber(omo.local_get(session_key))
if previous_session ~= office_started then
  omo.local_set(session_key, office_started)
  omo.local_delete("idle_since")
  omo.local_delete("fired")
end

-- The office-start grace period is independent of agent timestamps. It also
-- makes a missing or backwards clock fail closed.
if now < office_started or now - office_started < idle_after then
  return
end

local exempt = {}
for _, role in ipairs(settings.exempt_roles or {"ceo", "smokealarm"}) do
  exempt[role] = true
end

local active = false
for _, agent in ipairs(data.agents or {}) do
  if not exempt[agent.role] then
    active = true
    break
  end
end

if active then
  -- Do not count the last active-agent observation toward the next quiet
  -- interval: the first empty snapshot initializes a fresh idle_since.
  omo.local_delete("idle_since")
  omo.local_delete("fired")
  return
end

local idle_since = tonumber(omo.local_get("idle_since"))
local ceo_activity = tonumber(data.ceo_activity_at_unix)
if ceo_activity and ceo_activity > 0 then
  -- A timestamp from a clock ahead of the cron tick cannot establish elapsed
  -- time. Clamp it to this session and the current observation.
  local observed = math.max(office_started, math.min(ceo_activity, now))
  if not idle_since or observed > idle_since then
    idle_since = observed
    omo.local_set("idle_since", idle_since)
    omo.local_delete("fired")
  end
elseif not idle_since then
  idle_since = now
  omo.local_set("idle_since", idle_since)
end

if now < idle_since then
  idle_since = now
  omo.local_set("idle_since", idle_since)
end

local function log_status(message)
  local last = tonumber(omo.local_get("last_log_at"))
  if not last or now - last >= 300 then
    omo.log(message)
    omo.local_set("last_log_at", now)
  end
end

local elapsed = now - idle_since
if elapsed < idle_after then
  local remaining = math.ceil(idle_after - elapsed)
  log_status("autoshutdown: idle countdown; safe shutdown in " .. remaining .. "s")
  return
end

if omo.local_get("fired") == true then
  return
end

-- A shutdown already underway is terminal for this quiet period. Marking it
-- fired keeps later cron ticks quiet without issuing a second request.
if data.shutdown_in_progress == true then
  omo.local_set("fired", true)
  return
end

local reason = "autoshutdown: office idle for " .. idle_after_text .. " (no active agents, no CEO activity)"
local output, err = omo.exec(
  "env", "OMO_PLUGIN_NAME=", "OMO_PLUGIN_EVENT=",
  "omo", "safe-shutdown", "--reason", reason
)
if err and string.match(err, "%S") then
  local safe_error = string.gsub(tostring(err), "%c", "?")
  if #safe_error > 256 then
    safe_error = string.sub(safe_error, 1, 256)
  end
  omo.log("autoshutdown: safe-shutdown failed: " .. safe_error)
  return
end

omo.local_set("fired", true)
omo.log("autoshutdown: safe-shutdown requested")
