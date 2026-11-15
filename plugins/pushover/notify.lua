-- Sends one Pushover notification when a ticket needs the user's attention
-- or an agent asks the user a question. Questions to other roles send
-- nothing. A failed request is written to the plugin's log; the hook itself
-- does not fail, so a Pushover outage never marks the plugin as broken.
--
-- Ticket titles, attention texts and question bodies are written by people
-- and agents. They are only joined into the message as plain text (no
-- string.format, no patterns built from them) and cut to Pushover's limits.

local MAX_MESSAGE = 1024
local MAX_TITLE = 250

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

local form = {
  token = token,
  user = user,
  title = cut(heading, MAX_TITLE),
  message = cut("#" .. text(ticket.number) .. " " .. text(ticket.title) .. ": " .. reason, MAX_MESSAGE),
  priority = tostring(tonumber(settings.priority) or 0),
}
if text(settings.sound) ~= "" then
  form.sound = text(settings.sound)
end
local url = text(ticket.url)
if url ~= "" then
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
