-- Usage of an Antigravity CLI (agy) account.
--
-- Where the numbers come from: agy's own /usage command. omo runs the
-- profile's command as `agy -p /usage --output-format json` (omo.agy_usage)
-- in an empty folder; agy asks Google with its own login, which this script
-- never reads. The command runs no model turn and spends no quota.
--
-- Windows: agy meters its models in quota groups ("Gemini Models", "Claude
-- and GPT models"); the models of a group share its limits. Each limit
-- (bucket) of a group is one window, named by the bucket's id as agy
-- reports it (gemini-weekly, 3p-weekly; other characters as "_") and
-- labelled "<group>, <period>" ("Gemini models, weekly"). The members of
-- each group are written to the log, as agy names them. A model counts the
-- windows its usage_windows list; omo's agy profile lists each model's group
-- window.
--
-- An answer that says the service is rate limited (429, RESOURCE_EXHAUSTED)
-- ends the run with omo.rate_limited, so that omo waits before the next one.

local MAX_WINDOWS = 32 -- what omo accepts from one run

local PERIODS = { weekly = "weekly", daily = "daily", monthly = "monthly", hourly = "hourly" }

local function window_name(id, n)
  if type(id) ~= "string" then
    return "group_" .. n
  end
  local name = string.lower(id):gsub("[^a-z0-9_-]", "_")
  name = string.sub(name, 1, 40)
  if not string.find(name, "^[a-z0-9]") then
    return "group_" .. n
  end
  return name
end

local function group_label(group, bucket)
  local name = type(group.name) == "string" and group.name or "Quota group"
  -- "Gemini Models" reads "Gemini models", as "Claude and GPT models" does.
  name = string.gsub(name, "(%s)Models$", "%1models")
  local period = PERIODS[bucket.window]
  if not period then
    period = type(bucket.window) == "string" and bucket.window ~= "" and bucket.window
      or (type(bucket.name) == "string" and string.lower(bucket.name)) or "limit"
  end
  return name .. ", " .. period
end

local function rate_limited_answer(answer)
  local text = ""
  if type(answer) == "table" then
    text = tostring(answer.response or "") .. " " .. tostring(answer.error or "")
  elseif type(answer) == "string" then
    text = answer
  end
  return string.find(text, "429", 1, true) or string.find(text, "RESOURCE_EXHAUSTED", 1, true)
end

local function read_answer()
  local text, err = omo.agy_usage()
  if not text then
    error(err, 0)
  end
  local answer = omo.json_decode(text)
  if type(answer) ~= "table" then
    if rate_limited_answer(text) then
      omo.rate_limited(nil)
    end
    error("agy did not answer JSON", 0)
  end
  if answer.status ~= "SUCCESS" then
    if rate_limited_answer(answer) then
      omo.rate_limited(nil)
    end
    error("agy answered status " .. tostring(answer.status), 0)
  end
  local command = answer.command
  if type(command) ~= "table" or command.name ~= "usage" or type(command.data) ~= "table" then
    error("agy did not run its /usage command; this agy may be too old or too new", 0)
  end
  return command.data
end

local function used_percent(bucket)
  local remaining = bucket.remaining_fraction
  if type(remaining) ~= "number" or remaining ~= remaining then
    return nil
  end
  return math.min(math.max((1 - remaining) * 100, 0), 100)
end

function usage(account)
  local data = read_answer()
  local groups = data.groups
  if type(groups) ~= "table" then
    error("agy reported no quota groups", 0)
  end
  local windows, seen, n = {}, {}, 0
  for _, group in ipairs(groups) do
    if type(group) == "table" then
      if type(group.name) == "string" and type(group.description) == "string" then
        omo.log(group.name .. ": " .. group.description)
      end
      for _, bucket in ipairs(type(group.buckets) == "table" and group.buckets or {}) do
        n = n + 1
        local used = type(bucket) == "table" and used_percent(bucket)
        if not used then
          omo.log("skipped a limit of " .. tostring(group.name) .. " without a remaining fraction")
        elseif #windows >= MAX_WINDOWS then
          omo.log("skipped a limit of " .. tostring(group.name) .. ": too many windows")
        else
          local name = window_name(bucket.id, n)
          if seen[name] then
            name = window_name(name .. "_" .. n, n)
          end
          seen[name] = true
          local w = { name = name, label = group_label(group, bucket), used = used }
          if type(bucket.reset_time) == "string" then
            if omo.time_parse(bucket.reset_time) then
              w.resets_at = bucket.reset_time
            else
              omo.log("the reset time of " .. name .. " could not be read")
            end
          end
          table.insert(windows, w)
        end
      end
    end
  end
  if #windows == 0 then
    error("agy reported no usable limit", 0)
  end
  return windows
end
