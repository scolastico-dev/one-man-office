-- Sends one Pushover notification when a ticket needs the user's attention
-- or an agent asks the user a question. Questions to other roles send
-- nothing. A failed request is written to the plugin's log; the hook itself
-- does not fail, so a Pushover outage never marks the plugin as broken.
--
-- At most max_per_hour notifications (default 20) go out per hour, counted
-- from the first one of the hour in the plugin's store. The one after the
-- last allowed is a notice that further notifications are held back until
-- the hour is over; the rest of the hour sends nothing. The next hour's
-- first notification writes to the log how many were held back.
--
-- Ticket titles, attention texts and question bodies are written by people
-- and agents. They are only joined into the message as plain text (no
-- string.format, no patterns built from them) and cut to Pushover's limits.

local MAX_MESSAGE = 1024
local MAX_TITLE = 250
local HOUR = 3600
local DEFAULT_PER_HOUR = 20
local WINDOW_KEY = "rate_window"

-- cut returns s cut to at most limit UTF-8 characters, never inside one.
local function cut(s, limit)
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
  return string.sub(s, 1, index - 1)
end

local function text(v)
  if type(v) == "string" then
    return v
  end
  if type(v) == "number" then
    return tostring(v)
  end
  return ""
end

local data = event.data or {}
local ticket = data.ticket or {}
local reason, heading
if event.event == "ticket_needs_attention" then
  reason, heading = text(data.reason), "omo: a ticket needs your attention"
elseif event.event == "question_asked" then
  local comment = data.comment or {}
  if comment.to ~= "user" then
    return
  end
  reason, heading = text(comment.body), "omo: a question for you"
else
  return
end
if reason == "" then
  return
end

local settings = config or {}
local user, token = text(settings.user_key), text(settings.app_token)
if user == "" or token == "" then
  omo.log("pushover: not sent, user_key and app_token are not set in the plugin's configuration")
  return
end

local message = "#" .. text(ticket.number) .. " " .. text(ticket.title) .. ": " .. reason

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
    omo.log("pushover: " .. tostring(window.held) .. " notifications were held back in the last hour")
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
  -- This event's own notification is the first one held back.
  window.held = window.held + 1
  heading = "omo: notifications held back"
  message = "omo sent " .. tostring(per_hour) .. " notifications this hour; further notifications are held back until "
    .. os.date("%H:%M", window.start + HOUR) .. "."
end
omo.store_set(WINDOW_KEY, window)

local form = {
  token = token,
  user = user,
  title = cut(heading, MAX_TITLE),
  message = cut(message, MAX_MESSAGE),
  priority = tostring(tonumber(settings.priority) or 0),
}
if text(settings.sound) ~= "" then
  form.sound = text(settings.sound)
end
local url = text(ticket.url)
if url ~= "" and not notice then
  form.url = url
end

local api_url = text(settings.api_url)
if api_url == "" then
  api_url = "https://api.pushover.net/1/messages.json"
end
local response, failure = omo.http{ method = "POST", url = api_url, form = form }
if not response then
  omo.log("pushover: not sent: " .. text(failure))
  return
end
if response.status < 200 or response.status >= 300 then
  omo.log("pushover: not sent: Pushover answered with status " .. tostring(response.status))
end
