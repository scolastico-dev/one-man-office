local data = event.data or {}
if data.role ~= "ceo" then
  return
end

local note = "The pushover plugin is installed. To reach the user on their phone run `omo plugin trigger pushover notify -- \"<message>\"`. Use it for blockers and decisions that need the user; routine reports stay in mail."
local text = tostring(data.text or "")
if string.find(text, note, 1, true) == nil then
  data.text = text .. "\n\n" .. note
end
