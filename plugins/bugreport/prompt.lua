local data = event.data or {}
local settings = config or {}
local role = tostring(data.role or "")
local enabled = settings.instruct ~= false
local marker = "<!-- bugreport-instructions-v2 -->"
local roles = {
  user = true,
  ceo = true,
  product_manager = true,
  developer = true,
  reviewer = true,
  freelancer = true,
  firefighter = true
}

if enabled and roles[role] then
  local note = marker .. [[
Use the optional `bugreport` plugin only for omo failures such as wrong routing, a stuck lifecycle, a bad prompt, a crash, or a CLI error; it is not for project bugs (not project bugs). ALWAYS run `omo plugin trigger bugreport search -- "<query words>"` first and never duplicate an existing issue/report. Write an anonymized report with `## Summary`, `## Observed behavior`, `## Expected behavior`, `## Steps or evidence`, and `## Anonymization check`, then run `omo plugin trigger bugreport report -- body=<absolute-path> "<title>"`. Reports are local by default and never published without the user's consent; non-CEO agents mail the CEO the path, the CEO asks the user, and only then runs `omo plugin trigger bugreport publish -- <path>`.]]
  local text = tostring(data.text or "")
  if string.find(text, marker, 1, true) == nil then
    data.text = text .. "\n\n" .. note
  end
end
