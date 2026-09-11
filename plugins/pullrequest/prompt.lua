local data = event.data or {}
local settings = config or {}
local role = tostring(data.role or "")
local branch = tostring(data.branch or "")
local text = tostring(data.text or "")
local enabled = settings.instruct ~= false
local supported = role == "product_manager" or role == "developer" or role == "freelancer"
local pm_integrations = data.integration_branches or {}
local pm_with_integrations = role == "product_manager" and #pm_integrations > 0

if enabled and supported and ((data.merge_target == "asis" and string.match(branch, "%S")) or pm_with_integrations) then
	local once = pm_with_integrations and " once" or ""
	local note = "Before `omo done`, run" .. once .. " `omo plugin trigger pullrequest create -- \"<title>\"` and include the returned pull request URL in your done result."
  if string.find(text, note, 1, true) == nil then
    data.text = text .. "\n\n" .. note
  end
end
