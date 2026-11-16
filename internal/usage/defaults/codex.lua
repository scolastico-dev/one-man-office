-- Usage of a Codex (ChatGPT) subscription account.
--
-- Where the token comes from: the OAuth access token and account id Codex
-- keeps in auth.json (tokens.access_token, tokens.account_id) in its home
-- folder: CODEX_HOME, which is the profile's account folder when the
-- profile has one, else ~/.codex.
--
-- Where the token goes: only to chatgpt.com, in at most two requests. An
-- answer 429 (rate limited) ends the run (omo.rate_limited) so that omo
-- waits before the next one; nothing else is retried.
--
-- When the answer is 401 (the token expired), the script asks omo once to
-- let Codex refresh its own login: omo runs the profile's own command for
-- a moment until Codex has rewritten auth.json (omo never writes it), then
-- the script reads the file again and retries once.
--
-- Windows: each rate limit has a primary and a secondary window, named by
-- their length (limit_window_seconds): "session" for a few hours (1 to 12),
-- "weekly" for about a week (within 10 %), any other length
-- "window_<hours>h". A window that does not say its length is "session" as
-- the primary and "weekly" as the secondary window. The windows of each
-- additional limit are named "<limit name>_<window name>" (lowercase, other
-- characters as "_"). Each window carries a label for people: "Session",
-- "Weekly", "<hours> hours", and for an additional limit "<label>, <limit
-- name>" (the name as the answer writes it).

local USAGE_URL = "https://chatgpt.com/backend-api/wham/usage"
local WEEK = 604800
local MAX_WINDOWS = 32 -- what omo accepts from one run

local function trim(s)
  return (string.gsub(s, "^%s+", ""):gsub("%s+$", ""))
end

local function auth_path(account)
  local home = omo.env("CODEX_HOME")
  if home ~= nil then
    home = trim(home)
  end
  if home == nil or home == "" then
    home = account.home .. "/.codex"
  end
  return home .. "/auth.json"
end

local function read_tokens(path)
  local text, err = omo.read_file(path)
  if not text then
    error("no Codex credentials: " .. err, 0)
  end
  local auth = omo.json_decode(text)
  local tokens = type(auth) == "table" and auth.tokens
  if type(tokens) ~= "table" then
    error("the Codex credentials hold no OAuth access token", 0)
  end
  local access = tokens.access_token or tokens.accessToken
  local account_id = tokens.account_id or tokens.accountId
  if type(access) ~= "string" or access == "" then
    error("the Codex credentials hold no OAuth access token", 0)
  end
  if type(account_id) ~= "string" then
    account_id = nil
  end
  return access, account_id
end

local function fetch(path)
  local access, account_id = read_tokens(path)
  local headers = { Authorization = "Bearer " .. access }
  if account_id and account_id ~= "" then
    headers["ChatGPT-Account-Id"] = account_id
  end
  local res, err = omo.http({ url = USAGE_URL, headers = headers })
  if not res then
    error("usage request failed: " .. err, 0)
  end
  return res
end

local function is_window(w)
  return type(w) == "table" and type(w.used_percent) == "number" and w.used_percent >= 0
end

local HOUR = 3600

-- kind names a window by its length: name and label, nil when it does not
-- say its length (then its place decides).
local function kind(w, fallback)
  local secs = w.limit_window_seconds
  if type(secs) ~= "number" or secs <= 0 then
    if fallback == "weekly" then
      return "weekly", "Weekly"
    end
    return "session", "Session"
  end
  if math.abs(secs - WEEK) <= WEEK / 10 then
    return "weekly", "Weekly"
  end
  if secs >= HOUR and secs <= 12 * HOUR then
    return "session", "Session"
  end
  local hours = math.max(1, math.floor(secs / HOUR + 0.5))
  return "window_" .. hours .. "h", hours .. (hours == 1 and " hour" or " hours")
end

-- classify lists the usable windows of a rate limit as {name, label, w}:
-- primary first. When two windows get the same name the secondary one stays
-- (an older answer's weekly window was the secondary one).
local function classify(rl)
  local out = {}
  if type(rl) ~= "table" then
    return out
  end
  local byname = {}
  for _, pair in ipairs({ { rl.primary_window, "session" }, { rl.secondary_window, "weekly" } }) do
    local w = pair[1]
    if is_window(w) then
      local name, label = kind(w, pair[2])
      local item = { name = name, label = label, w = w }
      if byname[name] then
        omo.log("two windows of one rate limit are both '" .. name .. "'; the secondary one is reported")
        byname[name].w, byname[name].label = w, label
      else
        byname[name] = item
        table.insert(out, item)
      end
    end
  end
  return out
end

-- reset_at reads a window's reset time: Unix seconds or RFC 3339 text.
-- Nothing (or 0) means none; a value that cannot be read is logged, once.
local function reset_at(w, where)
  local v = w.reset_at
  if v == nil or v == 0 then
    return nil
  end
  if type(v) == "number" and v > 0 and v <= 1e11 then
    return v
  end
  if type(v) == "string" then
    local y, mo, d, h, mi, sec, rest = string.match(v, "^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)(.*)$")
    if y and tonumber(mo) >= 1 and tonumber(mo) <= 12 and tonumber(d) >= 1 and tonumber(d) <= 31
      and tonumber(h) <= 23 and tonumber(mi) <= 59 and tonumber(sec) <= 60 then
      local frac = string.match(rest, "^%.%d+(.*)$") or rest
      if frac == "Z" or string.match(frac, "^[+-]%d%d:%d%d$") then
        return v
      end
    end
  end
  omo.log("the reset time of " .. where .. " could not be read")
  return nil
end

local function window(name, label, w, where)
  return { name = name, label = label, used = w.used_percent, resets_at = reset_at(w, where) }
end

local function limit_name(name, suffix)
  local k = string.lower(name)
  k = string.gsub(k, "[^a-z0-9_-]", "_")
  k = string.sub(k, 1, 40 - #suffix - 1) .. "_" .. suffix
  if not string.match(k, "^[a-z0-9]") then
    return nil
  end
  return k
end

function usage(account)
  local path = auth_path(account)
  local res = fetch(path)
  if res.status == 401 then
    local ok, err = omo.codex_refresh(path)
    if not ok then
      error("the Codex login was refused and could not be refreshed: " .. err, 0)
    end
    res = fetch(path)
  end
  if res.status == 429 then
    -- Asked too often (omo and any other tool reading the same account):
    -- ends the run with "the usage API is rate limited; trying again
    -- later"; omo waits as long as the answer asks, or longer each time.
    omo.rate_limited(res.headers["Retry-After"])
  end
  if res.status < 200 or res.status > 299 then
    error("usage API returned HTTP " .. res.status, 0)
  end
  local body = omo.json_decode(res.body)
  if type(body) ~= "table" or type(body.rate_limit) ~= "table" then
    error("the usage answer has no rate limit", 0)
  end

  local windows, seen = {}, {}
  for _, item in ipairs(classify(body.rate_limit)) do
    seen[item.name] = true
    table.insert(windows, window(item.name, item.label, item.w, "the " .. item.name .. " window"))
  end

  local additional = body.additional_rate_limits
  if type(additional) == "table" then
    for _, limit in ipairs(additional) do
      if type(limit) == "table" and type(limit.limit_name) == "string" then
        for _, item in ipairs(classify(limit.rate_limit)) do
          local name = limit_name(limit.limit_name, item.name)
          if name and not seen[name] and #windows < MAX_WINDOWS then
            seen[name] = true
            table.insert(windows, window(name, item.label .. ", " .. limit.limit_name, item.w, "a window of an additional limit"))
          end
        end
      end
    end
  end
  if #windows == 0 then
    error("the usage answer has no valid window", 0)
  end
  return windows
end
