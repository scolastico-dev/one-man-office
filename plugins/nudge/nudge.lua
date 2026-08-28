-- The bundled nudge plugin doubles as a small Lua plugin example. It records
-- activity from lifecycle/log events, then uses cron's safe agent snapshot to
-- send durable mail reminders. It never reads omo.yaml or SQLite directly.

local event_name = event.event
local data = event.data

if event_name == "agent_start" or event_name == "agent_log_line" then
  if data.agent and data.at_unix then
    local key = "activity:" .. data.agent
    local previous = tonumber(omo.local_get(key)) or 0
    if event_name == "agent_start" or tonumber(data.at_unix) - previous >= 30 then
      omo.local_set(key, data.at_unix)
    end
  end
  return
end

if event_name ~= "cron" then
  return
end

local now = tonumber(data.at_unix) or 0

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
  local ok = omo.nudge(agent, message)
  if ok then
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

    if tonumber(agent.unread_messages) > 0 and idle >= 300 then
      remind(agent.name, "inbox",
        "You have unread office mail. Run `omo inbox`, respond as needed, then continue or use `omo wait` when parked.",
        900)
    elseif agent.role == "smokealarm" and idle >= 300 then
      remind(agent.name, "smokealarm_done",
        "This smoke-alarm round has been quiet for five minutes. Finish the check and report with `omo done`; smoke alarms must not use `omo wait`.",
        600)
    elseif agent.job_state == "done" and idle >= 120 then
      remind(agent.name, "park_completed",
        "Your job is already marked done. If your role is retained for follow-up, park with `omo wait`; otherwise make sure you reported completion with `omo done`.",
        900)
    elseif agent.role == "reviewer" and agent.job_state == "rework" and idle >= 300 then
      remind(agent.name, "reviewer_wait",
        "The rejected job is in rework. Stay available for developer questions by using `omo wait` instead of idling at the prompt.",
        900)
    elseif agent.role ~= "ceo" and tonumber(agent.job_id) == 0 and idle >= 900 then
      remind(agent.name, "no_job_wait",
        "No active job is attached and the session has been quiet. Use `omo wait` while parked so mail can wake you cleanly.",
        1800)
    elseif agent.role ~= "ceo" and idle >= 900 then
      remind(agent.name, "stale_work",
        "The session has been quiet for 15 minutes. Publish the current action with `omo step`; if the goal is complete use `omo done`, or use `omo wait` when intentionally parked.",
        1800)
    end
  end
end
