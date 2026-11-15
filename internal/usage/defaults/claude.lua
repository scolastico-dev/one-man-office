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
-- Where the token goes: only to api.anthropic.com, in one request.
--
-- Windows: every top-level object of the answer with a numeric
-- "utilization" is a window. five_hour is "session", seven_day is "weekly",
-- seven_day_<model> is "<model>_weekly" (seven_day_fable: "fable_weekly");
-- other keys stay as they are (lowercase, other characters as "_").

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

local function window_name(key)
  local k = string.lower(key)
  if k == "five_hour" then
    return "session"
  end
  if k == "seven_day" then
    return "weekly"
  end
  local model = string.match(k, "^seven_day_(.+)$")
  if model then
    k = string.sub(model, 1, 33) .. "_weekly"
  end
  k = string.gsub(k, "[^a-z0-9_-]", "_")
  k = string.sub(k, 1, 40)
  if not string.match(k, "^[a-z0-9]") then
    return nil
  end
  return k
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
  if res.status < 200 or res.status > 299 then
    error("usage API returned HTTP " .. res.status, 0)
  end
  local body = omo.json_decode(res.body)
  if type(body) ~= "table" then
    error("the usage answer is not a JSON object", 0)
  end

  local keys = {}
  for key in pairs(body) do
    table.insert(keys, key)
  end
  table.sort(keys)
  local windows, seen = {}, {}
  for _, key in ipairs(keys) do
    local value = body[key]
    if type(value) == "table" and type(value.utilization) == "number" then
      local name = window_name(key)
      if name and not seen[name] then
        seen[name] = true
        local resets = value.resets_at
        if type(resets) ~= "string" or resets == "" then
          resets = nil
        end
        table.insert(windows, { name = name, used = value.utilization, resets_at = resets })
      end
    end
  end
  if #windows == 0 then
    error("the usage answer holds no usage windows", 0)
  end
  return windows
end
