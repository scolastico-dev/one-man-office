-- The manual action "test": sends one fixed notification and tells the user
-- whether Pushover took it. A failure is the answer and a log line, not an
-- error of the plugin.

local function text(v)
  if type(v) == "string" then
    return v
  end
  return ""
end

local settings = config or {}
local user, token = text(settings.user_key), text(settings.app_token)
if user == "" or token == "" then
  return { message = "Not sent: set user_key and app_token in the plugin's configuration." }
end

local form = {
  token = token,
  user = user,
  title = "omo",
  message = "Test notification from omo.",
  priority = tostring(tonumber(settings.priority) or 0),
}
if text(settings.sound) ~= "" then
  form.sound = text(settings.sound)
end

local api_url = text(settings.api_url)
if api_url == "" then
  api_url = "https://api.pushover.net/1/messages.json"
end
local response, failure = omo.http{ method = "POST", url = api_url, form = form }
if not response then
  omo.log("pushover: test not sent: " .. text(failure))
  return { message = "Not sent: " .. text(failure) .. "." }
end
if response.status < 200 or response.status >= 300 then
  omo.log("pushover: test not sent: Pushover answered with status " .. tostring(response.status))
  return { message = "Not sent: Pushover answered with status " .. tostring(response.status) .. "." }
end
return { message = "Sent a test notification." }
