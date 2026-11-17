-- Posts a message to a Discord channel through its webhook when a ticket
-- needs the user's attention or an agent asks the user a question, and
-- answers the manual action "test" with one test message. A failed request
-- is written to the plugin's log; the hook itself does not fail, so a
-- Discord outage never marks the plugin as broken.
--
-- Safety:
--   * webhook_url must be https://discord.com/api/webhooks/<id>/<token>
--     (or discordapp.com, optionally with /api/v<n>/): nothing else is ever
--     requested, whatever the configuration says. The message goes to the
--     same webhook through API version 10.
--   * Ticket titles, attention texts and question bodies are written by
--     people and agents: every character Discord reads as markdown gets a
--     backslash, and allowed_mentions is always sent with an empty parse
--     list, so no text from a ticket can ping @everyone, @here, a role or a
--     user. Only the configured mention may ping, and only that one id.
--   * Discord's limits: content 2000 characters, embed title 256,
--     description 4096, username 80; longer text is cut with an ellipsis.
--
-- Retries: an answer 429 waits for Retry-After (or retry_after) and a 5xx
-- answer or a failed connection waits 1 s, then 2 s; at most three tries,
-- and never a wait over 5 s. Other 4xx answers are not retried: the same
-- request would be refused again.
--
-- At most max_per_hour messages (default 20) go out per hour, counted from
-- the first one of the hour in the plugin's store, as the pushover plugin
-- does: the one after the last allowed is a notice that further messages
-- are held back until the hour is over.

local MAX_CONTENT = 2000
local MAX_TITLE = 256
local MAX_DESCRIPTION = 4096
local MAX_USERNAME = 80
local HOUR = 3600
local DEFAULT_PER_HOUR = 20
local WINDOW_KEY = "rate_window"
local ATTEMPTS = 3
local MAX_WAIT = 5
local REQUEST_TIMEOUT = 8
local ATTENTION_COLOR = 15105570
local QUESTION_COLOR = 3447003
local DEFAULT_EVENTS = { ticket_needs_attention = true, question_asked = true }

local function text(v)
  if type(v) == "string" then
    return v
  end
  if type(v) == "number" then
    return tostring(v)
  end
  return ""
end

-- prefix returns the first limit UTF-8 characters of s and whether s was
-- longer.
local function prefix(s, limit)
  local length = #s
  local index, count = 1, 0
  while index <= length and count < limit do
    local byte = string.byte(s, index)
    local size = 1
    if byte >= 240 then
      size = 4
    elseif byte >= 224 then
      size = 3
    elseif byte >= 192 then
      size = 2
    end
    if index + size - 1 > length then
      size = 1
    end
    index = index + size
    count = count + 1
  end
  return string.sub(s, 1, index - 1), index <= length
end

-- cut returns s cut to at most limit characters, the last of them an
-- ellipsis when s was longer.
local function cut(s, limit)
  local head, longer = prefix(s, limit)
  if not longer then
    return head
  end
  head = prefix(s, limit - 1)
  return head .. "…"
end

-- escape puts a backslash before every character Discord reads as
-- markdown, a mention, a link, an emoji or a list.
local function escape(s)
  return (string.gsub(s, "[\\%*_~`|<>#%-%[%]%(%)%.:@!%+=]", "\\%0"))
end

-- escaped is s escaped and cut to limit characters without leaving a lone
-- backslash before the ellipsis.
local function escaped(s, limit)
  -- Cut first, so a long text is not escaped as a whole.
  local raw = prefix(s, limit)
  local e = escape(raw)
  local head, longer = prefix(e, limit)
  if not longer and #raw == #s then
    return head
  end
  head = prefix(e, limit - 1)
  local slashes = string.match(head, "\\*$")
  if #slashes % 2 == 1 then
    head = string.sub(head, 1, -2)
  end
  return head .. "…"
end

-- webhook returns the address messages go to for the configured URL, or
-- nil when it is no Discord webhook address.
local function webhook(url)
  if type(url) ~= "string" then
    return nil
  end
  local host, id, token = string.match(url, "^https://([%w%.]+)/api/webhooks/(%d+)/([%w_%-]+)$")
  if not host then
    host, id, token = string.match(url, "^https://([%w%.]+)/api/v%d+/webhooks/(%d+)/([%w_%-]+)$")
  end
  if not host or (host ~= "discord.com" and host ~= "discordapp.com") then
    return nil
  end
  if #id < 15 or #id > 25 or #token > 200 then
    return nil
  end
  return "https://" .. host .. "/api/v10/webhooks/" .. id .. "/" .. token
end

-- mention returns the text that pings the configured role or user and the
-- allowed_mentions that lets exactly that one ping; for no mention an empty
-- text. A value that is no mention returns nil.
local function mention(v)
  v = text(v)
  local allowed = { parse = {} }
  if v == "" then
    return "", allowed
  end
  local kind, id = string.match(v, "^(%a+):(%d+)$")
  if not id or #id < 15 or #id > 25 then
    return nil
  end
  if kind == "role" then
    allowed.roles = { id }
    return "<@&" .. id .. ">", allowed
  elseif kind == "user" then
    allowed.users = { id }
    return "<@" .. id .. ">", allowed
  end
  return nil
end

-- username returns the configured name when Discord takes it, else nil.
local function username(v)
  v = text(v)
  if v == "" or string.find(v, "[%c]") then
    return nil
  end
  local lower = string.lower(v)
  if string.find(lower, "discord", 1, true) or string.find(lower, "clyde", 1, true) then
    return nil
  end
  return cut(v, MAX_USERNAME)
end

local function header(headers, name)
  if type(headers) ~= "table" then
    return nil
  end
  local values = headers[name]
  if type(values) == "table" then
    return values[1]
  end
  return nil
end

-- wait_of is how long a 429 answer asks to wait, in seconds.
local function wait_of(response)
  local seconds = tonumber(header(response.headers, "Retry-After"))
  if not seconds then
    local ok, body = pcall(omo.json_decode, response.body)
    if ok and type(body) == "table" then
      seconds = tonumber(body.retry_after)
    end
  end
  return seconds or 1
end

local function refusal(status)
  if status == 401 or status == 403 or status == 404 then
    return "Discord answered with status " .. tostring(status) .. ": the webhook does not exist any more or its address is wrong"
  end
  return "Discord answered with status " .. tostring(status)
end

-- post sends payload; it returns true, or nil and why not.
local function post(url, payload)
  local why
  for attempt = 1, ATTEMPTS do
    local response, failure = omo.http{ method = "POST", url = url, json = payload, timeout = REQUEST_TIMEOUT }
    local wait
    if response and response.status >= 200 and response.status < 300 then
      return true
    elseif not response then
      why, wait = text(failure), attempt
    elseif response.status == 429 then
      why, wait = "Discord answered with status 429 (rate limited)", wait_of(response)
    elseif response.status >= 500 then
      why, wait = "Discord answered with status " .. tostring(response.status), attempt
    else
      return nil, refusal(response.status)
    end
    if attempt == ATTEMPTS then
      break
    end
    if wait > MAX_WAIT then
      return nil, why .. "; it asks to wait " .. tostring(wait) .. " s, longer than this plugin waits"
    end
    if wait < 0.1 then
      wait = 0.1
    end
    omo.log("discord: " .. why .. "; trying again in " .. tostring(wait) .. " s")
    omo.sleep(wait)
  end
  return nil, why .. " " .. tostring(ATTEMPTS) .. " times"
end

local settings = config or {}
local url = webhook(settings.webhook_url)
local data = event.data or {}

if event.event == "manual" then
  if text(settings.webhook_url) == "" then
    return { message = "Not sent: set webhook_url in the plugin's configuration." }
  end
  if not url then
    return { message = "Not sent: webhook_url must be a Discord webhook address, https://discord.com/api/webhooks/<id>/<token>." }
  end
  local ping, allowed = mention(settings.mention)
  if not ping then
    return { message = "Not sent: mention must be empty, role:<id> or user:<id>." }
  end
  local payload = { content = cut((ping ~= "" and ping .. " " or "") .. "Test message from omo.", MAX_CONTENT), allowed_mentions = allowed }
  payload.username = username(settings.username)
  local ok, why = post(url, payload)
  if not ok then
    omo.log("discord: test not sent: " .. why)
    return { message = "Not sent: " .. why .. "." }
  end
  return { message = "Sent a test message." }
end

-- Which events send.
local wanted = DEFAULT_EVENTS
if type(settings.events) == "table" then
  wanted = {}
  for _, name in ipairs(settings.events) do
    if type(name) == "string" then
      wanted[name] = true
    end
  end
end
if not wanted[event.event] then
  return
end

local ticket = data.ticket or {}
local reason, heading, color
if event.event == "ticket_needs_attention" then
  reason, heading, color = text(data.reason), "A ticket needs your attention", ATTENTION_COLOR
elseif event.event == "question_asked" then
  local comment = data.comment or {}
  if comment.to ~= "user" then
    return
  end
  reason, heading, color = text(comment.body), "A question for you", QUESTION_COLOR
else
  return
end
if reason == "" then
  return
end

if text(settings.webhook_url) == "" then
  omo.log("discord: not sent, webhook_url is not set in the plugin's configuration")
  return
end
if not url then
  omo.log("discord: not sent, webhook_url is not a Discord webhook address (https://discord.com/api/webhooks/<id>/<token>)")
  return
end
local ping, allowed = mention(settings.mention)
if not ping then
  omo.log("discord: mention must be empty, role:<id> or user:<id>; sent without a mention")
  ping, allowed = mention("")
end

-- The hourly cap.
local per_hour = math.floor(tonumber(settings.max_per_hour) or DEFAULT_PER_HOUR)
if per_hour < 1 then
  per_hour = DEFAULT_PER_HOUR
end
local now = os.time()
local window = omo.store_get(WINDOW_KEY)
if type(window) ~= "table" or type(window.start) ~= "number" or type(window.sent) ~= "number"
    or now < window.start or now - window.start >= HOUR then
  if type(window) == "table" and type(window.held) == "number" and window.held > 0 then
    omo.log("discord: " .. tostring(window.held) .. " messages were held back in the last hour")
  end
  window = { start = now, sent = 0, held = 0 }
end
if type(window.held) ~= "number" then
  window.held = 0
end
if window.sent > per_hour then
  window.held = window.held + 1
  omo.store_set(WINDOW_KEY, window)
  return
end
window.sent = window.sent + 1
local notice = window.sent > per_hour
if notice then
  window.held = window.held + 1
end
omo.store_set(WINDOW_KEY, window)

local lead = ping ~= "" and ping .. " " or ""
local payload = { allowed_mentions = allowed, username = username(settings.username) }
if notice then
  payload.content = cut(lead .. "omo sent " .. tostring(per_hour) .. " messages this hour; further messages are held back until "
    .. os.date("%H:%M", window.start + HOUR) .. ".", MAX_CONTENT)
else
  payload.content = cut(lead .. heading, MAX_CONTENT)
  local embed = {
    title = escaped("#" .. text(ticket.number) .. " " .. text(ticket.title), MAX_TITLE),
    description = escaped(reason, MAX_DESCRIPTION),
    color = color,
  }
  local link = text(ticket.url)
  if string.match(link, "^https?://") then
    embed.url = link
  end
  if type(event.at) == "string" and event.at ~= "" then
    embed.timestamp = event.at
  end
  payload.embeds = { embed }
end

local ok, why = post(url, payload)
if not ok then
  omo.log("discord: not sent: " .. why)
end
