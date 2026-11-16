-- Usage of a Claude Code subscription account.
--
-- Where the token comes from: the OAuth access token Claude Code keeps in
-- .credentials.json (claudeAiOauth.accessToken) in its credential folder.
-- That folder is, as Claude Code itself picks it:
--   1. CLAUDE_SECURESTORAGE_CONFIG_DIR when it is set (set but empty: the
--      default folder below);
--   2. else CLAUDE_CONFIG_DIR, which is the profile's account folder when
--      the profile has one;
--   3. else ~/.claude.
-- On macOS, when that file does not exist, the token is read from Claude
-- Code's keychain entry "Claude Code-credentials" (for a folder set in 1 or
-- 2: "Claude Code-credentials-" and the first 8 hex digits of the SHA-256
-- of the folder's name).
--
-- Where the token goes: only to api.anthropic.com, in one request. The
-- script never retries: the usage API is rate limited, and an answer 429
-- ends the run (omo.rate_limited) so that omo waits before the next one.
--
-- Windows, from an answer with a "limits" list (what the API sends now):
--   kind "session"        -> "session"       label "Session (5 hours)"
--   kind "weekly_all"     -> "weekly"        label "Weekly, all models"
--   kind "weekly_scoped"  -> "<model>_weekly" label "Weekly, <model>", the
--     model's display name (else its id) lowercase with other characters as
--     "_" (Fable: "fable_weekly"); a scoped limit without a model but with a
--     surface is "surface_<surface>_weekly" ("Weekly, <surface>"); one that
--     names neither is skipped with a log line.
-- "percent" is the share used, "resets_at" the reset time. A limit of
-- another kind, without a percentage or with a name already taken is
-- skipped with a log line, never an error.
-- From an answer without that list (older answers): every top-level object
-- with a numeric "utilization" is a window; five_hour is "session",
-- seven_day is "weekly", seven_day_<model> is "<model>_weekly"; other keys
-- stay as they are (lowercase, other characters as "_"). A key whose value
-- is null gives no window.
-- In both: extra_usage (paid usage beyond the plan, not a limit of a model)
-- is the window "extra_usage" from its "utilization", labelled "Extra usage
-- credits (1.32 of 5.00 EUR)" when the answer has the amounts; it has no
-- reset time. A model that counts a window the answer lacks is judged by
-- the windows there are, and omo says when none of its windows is there.

local USAGE_URL = "https://api.anthropic.com/api/oauth/usage"
local SERVICE = "Claude Code-credentials"

local function trim(s)
  return (string.gsub(s, "^%s+", ""):gsub("%s+$", ""))
end

-- credential_root returns the credential folder and, when it was set
-- explicitly, the text that names its keychain entry.
local function credential_root(account)
  local default = account.home .. "/.claude"
  local secure = omo.env("CLAUDE_SECURESTORAGE_CONFIG_DIR")
  if secure ~= nil then
    if secure == "" then
      return default, nil
    end
    return secure, secure
  end
  local configured = omo.env("CLAUDE_CONFIG_DIR")
  if configured ~= nil and trim(configured) ~= "" then
    return trim(configured), configured
  end
  return default, nil
end

local function read_credentials(account)
  local root, named = credential_root(account)
  local text, err = omo.read_file(root .. "/.credentials.json")
  if text then
    return text
  end
  if err == "file not found" and account.os == "darwin" then
    local service = SERVICE
    if named then
      service = SERVICE .. "-" .. string.sub(omo.sha256_hex(omo.nfc(named)), 1, 8)
    end
    local secret = omo.keychain(service)
    if secret then
      return secret
    end
  end
  error("no Claude credentials: " .. err, 0)
end

local SESSION_LABEL = "Session (5 hours)"
local WEEKLY_LABEL = "Weekly, all models"

-- clean makes text a part of a window name: lowercase, every character
-- other than a-z, 0-9, "_" and "-" as "_", at most n characters; nil when
-- it does not start with a letter or digit.
local function clean(text, n)
  local k = string.gsub(string.lower(text), "[^a-z0-9_-]", "_")
  k = string.sub(k, 1, n)
  if not string.match(k, "^[a-z0-9]") then
    return nil
  end
  return k
end

-- text returns v when it is a non-empty string.
local function text(v)
  if type(v) == "string" and v ~= "" then
    return v
  end
  return nil
end

local function reset_time(v)
  if type(v) ~= "string" or v == "" then
    return nil
  end
  return v
end

-- old_window names and labels a top-level key of an older answer.
local function old_window(key)
  local k = string.lower(key)
  if k == "five_hour" then
    return "session", SESSION_LABEL
  end
  if k == "seven_day" then
    return "weekly", WEEKLY_LABEL
  end
  local model = string.match(k, "^seven_day_(.+)$")
  if model then
    local name = clean(model, 33)
    if not name then
      return nil
    end
    local shown = string.sub(key, #"seven_day_" + 1)
    shown = string.upper(string.sub(shown, 1, 1)) .. string.sub(shown, 2)
    return name .. "_weekly", "Weekly, " .. shown
  end
  return clean(k, 40), nil
end

-- scope_name names a weekly_scoped limit by its model, else its surface.
local function scope_name(scope)
  if type(scope) ~= "table" then
    return nil
  end
  local model = scope.model
  if type(model) == "table" then
    local shown = text(model.display_name) or text(model.id)
    local name = shown and clean(shown, 33)
    if name then
      return name .. "_weekly", "Weekly, " .. shown
    end
  end
  local surface = scope.surface
  local shown = text(surface)
  if type(surface) == "table" then
    shown = text(surface.display_name) or text(surface.name) or text(surface.id)
  end
  local name = shown and clean(shown, 25)
  if name then
    return "surface_" .. name .. "_weekly", "Weekly, " .. shown
  end
  return nil
end

-- money formats an amount in minor units ("1.32" for 132 with 2 places).
local function money(minor, places)
  return string.format("%." .. places .. "f", minor / 10 ^ places)
end

-- extra_window is the window of the paid extra usage, or nil.
local function extra_window(extra)
  if type(extra) ~= "table" or type(extra.utilization) ~= "number" then
    return nil
  end
  local label = "Extra usage credits"
  local used, limit, currency = extra.used_credits, extra.monthly_limit, text(extra.currency)
  local places = extra.decimal_places
  if type(places) ~= "number" or places ~= math.floor(places) or places < 0 or places > 6 then
    places = 2
  end
  if type(used) == "number" and type(limit) == "number" and currency then
    label = label .. " (" .. money(used, places) .. " of " .. money(limit, places) .. " " .. currency .. ")"
  end
  return { name = "extra_usage", label = label, used = extra.utilization }
end

-- from_limits reads the windows of an answer's limits list.
local function from_limits(limits)
  local windows, seen = {}, {}
  for _, limit in ipairs(limits) do
    if type(limit) == "table" then
      local kind = tostring(limit.kind)
      local name, label
      if kind == "session" then
        name, label = "session", SESSION_LABEL
      elseif kind == "weekly_all" then
        name, label = "weekly", WEEKLY_LABEL
      elseif kind == "weekly_scoped" then
        name, label = scope_name(limit.scope)
        if not name then
          omo.log("skipped a weekly_scoped limit that names neither a model nor a surface")
        end
      else
        omo.log("skipped the usage limit of unknown kind " .. kind)
      end
      if name then
        if type(limit.percent) ~= "number" then
          omo.log("skipped a " .. kind .. " limit without a percentage")
        elseif seen[name] then
          omo.log("skipped a second " .. kind .. " limit for " .. name)
        else
          seen[name] = true
          table.insert(windows, { name = name, label = label, used = limit.percent, resets_at = reset_time(limit.resets_at) })
        end
      end
    end
  end
  return windows
end

-- from_keys reads the windows of an older answer from its top-level keys:
-- session first, then weekly, then the others by name.
local function from_keys(body)
  local keys = {}
  for key in pairs(body) do
    if key ~= "extra_usage" then
      table.insert(keys, key)
    end
  end
  table.sort(keys)
  local windows, seen = {}, {}
  for _, key in ipairs(keys) do
    local value = body[key]
    if type(value) == "table" and type(value.utilization) == "number" then
      local name, label = old_window(key)
      if name and not seen[name] then
        seen[name] = true
        table.insert(windows, { name = name, label = label, used = value.utilization, resets_at = reset_time(value.resets_at) })
      end
    end
  end
  local rank = { session = 1, weekly = 2 }
  table.sort(windows, function(a, b)
    local ra, rb = rank[a.name] or 3, rank[b.name] or 3
    if ra ~= rb then
      return ra < rb
    end
    return a.name < b.name
  end)
  return windows
end

function usage(account)
  local creds = omo.json_decode(read_credentials(account))
  local oauth = type(creds) == "table" and creds.claudeAiOauth
  local token = type(oauth) == "table" and oauth.accessToken
  if type(token) ~= "string" or token == "" then
    error("the Claude credentials hold no OAuth access token", 0)
  end

  local res, err = omo.http({
    url = USAGE_URL,
    headers = {
      Authorization = "Bearer " .. token,
      ["anthropic-beta"] = "oauth-2025-04-20",
    },
  })
  if not res then
    error("usage request failed: " .. err, 0)
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
  if type(body) ~= "table" then
    error("the usage answer is not a JSON object", 0)
  end

  local windows
  if type(body.limits) == "table" and #body.limits > 0 then
    windows = from_limits(body.limits)
  else
    windows = from_keys(body)
  end
  local extra = extra_window(body.extra_usage)
  if extra and #windows < 32 then
    table.insert(windows, extra)
  end
  if #windows == 0 then
    error("the usage answer holds no usage windows", 0)
  end
  return windows
end
