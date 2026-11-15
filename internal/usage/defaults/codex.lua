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
-- Windows: rate_limit.primary_window is "session" (unless it is the weekly
-- window); the weekly window (the one that lasts seven days, else the
-- secondary window) is "weekly"; the weekly window of each additional limit
-- is "<limit name>_weekly" (lowercase, other characters as "_").

local USAGE_URL = "https://chatgpt.com/backend-api/wham/usage"
local WEEK = 604800

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
  return type(w) == "table" and type(w.used_percent) == "number"
end

-- weekly picks the seven-day window of a rate limit: the one whose length
-- is a week, else a secondary window that does not say its length.
local function weekly(rl)
  if type(rl) ~= "table" then
    return nil
  end
  local secondary, primary = rl.secondary_window, rl.primary_window
  if type(secondary) == "table" and secondary.limit_window_seconds == WEEK then
    return secondary
  end
  if type(primary) == "table" and primary.limit_window_seconds == WEEK then
    return primary
  end
  if type(secondary) == "table" and (secondary.limit_window_seconds == nil or secondary.limit_window_seconds == 0) then
    return secondary
  end
  return nil
end

local function window(name, w)
  local resets = w.reset_at
  if type(resets) ~= "number" or resets <= 0 then
    resets = nil
  end
  return { name = name, used = w.used_percent, resets_at = resets }
end

local function limit_name(name)
  local k = string.lower(name)
  k = string.gsub(k, "[^a-z0-9_-]", "_")
  k = string.sub(k, 1, 33) .. "_weekly"
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
  local week = weekly(body.rate_limit)
  if not is_window(week) then
    error("the usage answer has no valid weekly window", 0)
  end
  local primary = body.rate_limit.primary_window
  if primary ~= week and is_window(primary) then
    table.insert(windows, window("session", primary))
    seen.session = true
  end
  table.insert(windows, window("weekly", week))
  seen.weekly = true

  local additional = body.additional_rate_limits
  if type(additional) == "table" then
    for _, limit in ipairs(additional) do
      if type(limit) == "table" and type(limit.limit_name) == "string" then
        local name = limit_name(limit.limit_name)
        local w = weekly(limit.rate_limit)
        if name and not seen[name] and is_window(w) then
          seen[name] = true
          table.insert(windows, window(name, w))
        end
      end
    end
  end
  return windows
end
