local data = event.data or {}
local settings = config or {}
local role = tostring(data.role or "")
local branch = tostring(data.branch or "")
local text = tostring(data.text or "")
local enabled = settings.instruct ~= false
local developer_like = role == "developer" or role == "freelancer"

if enabled and role == "product_manager" then
	local note = "If completion leaves pull-request branches, before `omo done`, run once `omo plugin trigger pullrequest create -- \"<title>\"` and include every returned pull request URL in your done result."
	if string.find(text, note, 1, true) == nil then
		data.text = text .. "\n\n" .. note
	end
elseif enabled and developer_like and data.merge_target == "asis" and string.match(branch, "%S") then
	local note = "Before `omo done`, run `omo plugin trigger pullrequest create -- \"<title>\"` and include the returned pull request URL in your done result."
	if string.find(text, note, 1, true) == nil then
		data.text = text .. "\n\n" .. note
  end
end
