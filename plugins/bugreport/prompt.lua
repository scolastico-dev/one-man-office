local data = event.data or {}
local settings = config or {}
local role = tostring(data.role or "")
local enabled = settings.instruct ~= false
local marker = "<!-- bugreport-instructions-v1 -->"
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
Use the optional `bugreport` plugin only for omo failures such as wrong routing, a stuck lifecycle, a bad prompt, a crash, or a CLI error; it is not for project bugs. Reports must be anonymized and contain `## Summary`, `## Observed behavior`, `## Expected behavior`, `## Steps or evidence`, and `## Anonymization check`. Run `omo plugin trigger bugreport report -- body=<absolute-path> "<title>"` after writing the report.]]
  local text = tostring(data.text or "")
  if string.find(text, marker, 1, true) == nil then
    data.text = text .. "\n\n" .. note
  end
end
