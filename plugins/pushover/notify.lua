local settings = config or {}
local data = event.data or {}
local args = data.args or {}

if #args < 1 or #args > 2 or type(args[1]) ~= "string" or string.match(args[1], "%S") == nil then
  error("pushover notify requires one message and an optional title")
end
if tostring(settings.app_token or "") == "" or tostring(settings.user_key or "") == "" then
  error("pushover credentials are not configured")
end

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

local function caller_display(role)
  if role == "ceo" then
    return "CEO"
  end
  if role == "user" then
    return "user"
  end
  local readable = string.gsub(role, "_", " ")
  return string.gsub(readable, "^%l", string.upper)
end

local title = "omo " .. caller_display(tostring(data.caller_role or "user"))
if args[2] ~= nil and string.match(args[2], "%S") ~= nil then
  title = title .. ": " .. args[2]
end
local form = {
  token = tostring(settings.app_token),
  user = tostring(settings.user_key),
  title = truncate(title, 250),
  message = truncate(args[1], 1024),
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
  omo.log("pushover notification failed: transport error")
  error("pushover notification failed")
end
if response.status < 200 or response.status >= 300 then
  omo.log("pushover notification failed: status " .. tostring(response.status) .. ": errors: " .. errors_from_body(response.body))
  error("pushover notification failed")
end
