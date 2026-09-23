local settings = config or {}
local data = event.data or {}

local function replace_plain(value, old, new)
  if old == nil or old == "" then
    return value
  end
  local output = {}
  local start = 1
  while true do
    local first, last = string.find(value, old, start, true)
    if not first then
      table.insert(output, string.sub(value, start))
      return table.concat(output)
    end
    table.insert(output, string.sub(value, start, first - 1))
    table.insert(output, new)
    start = last + 1
  end
end

local function redact(value)
  value = replace_plain(value, tostring(settings.app_token or ""), "[redacted]")
  return replace_plain(value, tostring(settings.user_key or ""), "[redacted]")
end

local function missing_credentials()
  if tostring(settings.app_token or "") ~= "" and tostring(settings.user_key or "") ~= "" then
    return false
  end
  local started = tostring(data.office_started_at_unix or 0)
  if omo.local_get("missing_credentials_started_at") ~= started then
    omo.local_set("missing_credentials_started_at", started)
    omo.log("pushover credentials are not configured")
  end
  return true
end

local function truncate(value, limit)
  local length = #value
  local index = 1
  local count = 0
  while index <= length and count < limit do
    local byte = string.byte(value, index)
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
  if index <= length then
    return string.sub(value, 1, index - 1)
  end
  return value
end

local function errors_from_body(body)
  local array = string.match(body or "", '"errors"%s*:%s*%[(.-)%]')
  if not array then
    return "errors unavailable"
  end
  local values = {}
  for value in string.gmatch(array, '"([^"\\]*)"') do
    table.insert(values, redact(value))
  end
  if #values == 0 then
    return "errors unavailable"
  end
  return table.concat(values, ", ")
end

local function send(message, title)
  local form = {
    token = tostring(settings.app_token),
    user = tostring(settings.user_key),
    title = truncate(title, 250),
    message = truncate(message, 1024),
    priority = tostring(settings.priority or 0)
  }
  if tostring(settings.sound or "") ~= "" then
    form.sound = tostring(settings.sound)
  end
  local response, transport_error = omo.http{
    method = "POST",
    url = tostring(settings.api_url or "https://api.pushover.net/1/messages.json"),
    form = form
  }
  if not response then
    return false, "transport error"
  end
  if response.status >= 200 and response.status < 300 then
    return true, ""
  end
  return false, "status " .. tostring(response.status) .. ": errors: " .. errors_from_body(response.body)
end

if missing_credentials() then
  return
end

local messages = data.user_inbox or {}
local by_id = {}
local ids = {}
for _, message in ipairs(messages) do
  local id = tostring(message.id or "")
  if id ~= "" and by_id[id] == nil then
    by_id[id] = message
    table.insert(ids, id)
  end
end
table.sort(ids)

if #ids == 0 then
  omo.local_delete("inbox_signature")
  omo.local_delete("window_started_at")
  omo.local_delete("notified")
  return
end

local signature_parts = {}
for _, id in ipairs(ids) do
  table.insert(signature_parts, tostring(#id) .. ":" .. id)
end
local signature = table.concat(signature_parts, ";")
local previous = omo.local_get("inbox_signature")
local notified = omo.local_get("notified") == true
if previous == nil then
  omo.local_set("inbox_signature", signature)
  omo.local_set("window_started_at", tonumber(data.at_unix) or 0)
  return
end
if previous ~= signature then
  omo.local_set("inbox_signature", signature)
  if not notified then
    omo.local_set("window_started_at", tonumber(data.at_unix) or 0)
  end
  return
end
if notified then
  return
end

local started = tonumber(omo.local_get("window_started_at"))
local now = tonumber(data.at_unix) or 0
if not started or now - started < omo.duration(settings.stable_window or "5m") then
  return
end

local count = #ids
local message = tostring(count) .. " unread message"
if count ~= 1 then
  message = message .. "s"
end
message = message .. " for you in office " .. tostring(data.office_path or "")
for index = 1, math.min(5, count) do
  local item = by_id[ids[index]]
  message = message .. "\nfrom " .. tostring(item.from or "") .. ": " .. tostring(item.subject or "")
end

local ok, reason = send(message, "omo inbox")
if ok then
  omo.local_set("notified", true)
else
  omo.log("pushover notification failed: " .. redact(reason))
end
