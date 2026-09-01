-- The bundled nudge plugin doubles as a small Lua plugin example. It records
-- activity from lifecycle/log events, then uses cron's safe agent snapshot to
-- send durable mail reminders. It never reads omo.yaml or SQLite directly.

local event_name = event.event
local data = event.data
local settings = config or {}
local reminder_settings = settings.reminders or {}

local function duration(value, fallback)
  if value == nil then
    return fallback
  end
  return omo.duration(value)
end

local function timing(kind, field, fallback)
  local reminder = reminder_settings[kind] or {}
  return duration(reminder[field], fallback)
end

if event_name == "agent_start" or event_name == "agent_log_line" then
  if data.agent and data.at_unix then
    local key = "activity:" .. data.agent
    local previous = tonumber(omo.local_get(key)) or 0
    local sample_interval = duration(settings.activity_sample_interval, 30)
    if event_name == "agent_start" or tonumber(data.at_unix) - previous >= sample_interval then
      omo.local_set(key, data.at_unix)
    end
  end
  return
end

if event_name ~= "cron" then
  return
end

local now = tonumber(data.at_unix) or 0

if data.snapshot_error then
  return
end

-- Agent names are never reused. Remove activity and cooldown values once an
-- agent leaves the live snapshot so long-running offices do not accumulate
-- plugin-local state forever. Unrelated user-added keys remain untouched.
local living = {}
for _, agent in ipairs(data.agents or {}) do
  living[agent.name] = true
end
for _, key in ipairs(omo.local_keys("activity:")) do
  local agent = string.sub(key, string.len("activity:") + 1)
  if not living[agent] then
    omo.local_delete(key)
  end
end
for _, key in ipairs(omo.local_keys("last_nudge:")) do
  local agent = string.match(key, "^last_nudge:([^:]+):")
  if agent and not living[agent] then
    omo.local_delete(key)
  end
end

local function due(agent, kind, cooldown)
  local key = "last_nudge:" .. agent .. ":" .. kind
  local last = tonumber(omo.local_get(key)) or 0
  if last == 0 then
    return true
  end
  if now - last < cooldown then
    return false
  end
  return true
end

local function remind(agent, kind, message, cooldown)
  if not due(agent, kind, cooldown) then
    return
  end
  local _, err = omo.exec("omo", "send", "--to", agent,
    "--subject", "Workflow reminder", message)
  if err == "" then
    omo.local_set("last_nudge:" .. agent .. ":" .. kind, now)
  end
end

for _, agent in ipairs(data.agents or {}) do
  if agent.state == "working" then
    local activity = math.max(
      tonumber(omo.local_get("activity:" .. agent.name)) or 0,
      tonumber(agent.step_updated_at_unix) or 0,
      tonumber(agent.created_at_unix) or 0)
    if activity == 0 then
      activity = now
    end
    local idle = now - activity

    if tonumber(agent.unread_messages) > 0 and idle >= timing("inbox", "after", 300) then
      remind(agent.name, "inbox",
        "You have unread office mail. Run `omo inbox`, respond as needed, then continue or use `omo wait` when parked.",
        timing("inbox", "repeat", 900))
    elseif agent.role == "smokealarm" and idle >= timing("smokealarm_done", "after", 300) then
      remind(agent.name, "smokealarm_done",
        "This smoke-alarm round has been quiet past its configured limit. Finish the check and report with `omo done`; smoke alarms must not use `omo wait`.",
        timing("smokealarm_done", "repeat", 600))
    elseif agent.job_state == "done" and idle >= timing("park_completed", "after", 120) then
      remind(agent.name, "park_completed",
        "Your job is already marked done. If your role is retained for follow-up, park with `omo wait`; otherwise make sure you reported completion with `omo done`.",
        timing("park_completed", "repeat", 900))
    elseif agent.role == "reviewer" and agent.job_state == "rework" and idle >= timing("reviewer_wait", "after", 300) then
      remind(agent.name, "reviewer_wait",
        "The rejected job is in rework. Stay available for developer questions by using `omo wait` instead of idling at the prompt.",
        timing("reviewer_wait", "repeat", 900))
    elseif agent.role ~= "ceo" and tonumber(agent.job_id) == 0 and idle >= timing("no_job_wait", "after", 900) then
      remind(agent.name, "no_job_wait",
        "No active job is attached and the session has been quiet. Use `omo wait` while parked so mail can wake you cleanly.",
        timing("no_job_wait", "repeat", 1800))
    elseif agent.role ~= "ceo" and idle >= timing("stale_work", "after", 900) then
      remind(agent.name, "stale_work",
        "The session has been quiet past its configured limit. Publish the current action with `omo step`; if the goal is complete use `omo done`, or use `omo wait` when intentionally parked.",
        timing("stale_work", "repeat", 1800))
    end
  end
end
