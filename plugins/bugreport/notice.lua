local data = event.data or {}

local function trim(value)
  return string.gsub(tostring(value or ""), "^%s*(.-)%s*$", "%1")
end

local function fail(message)
  error("bugreport: " .. message)
end

local function exec(...)
  local output, exec_error = omo.exec(...)
  if exec_error ~= "" then
    return nil, exec_error
  end
  return output, nil
end

local args = data.args or {}
local message = table.concat(args, " ")
if trim(message) == "" then
  fail("notice message must not be empty")
end

local block = "[bugreport] The user reports a possible problem with omo itself: " .. message ..
  ". Investigate with `omo job list`, `omo logs`, events, and your own transcript. Then write a detailed report in storage that describes ONLY omo's behaviour: no project names, paths, repository or branch names, customer data, secrets, or mail contents. Use the headings ## Summary, ## Observed behavior, ## Expected behavior, ## Steps or evidence, and ## Anonymization check, and run `omo plugin trigger bugreport report -- body=<absolute path> \"<title>\"`. Reply to the user with the result line."
if #block >= 64 * 1024 then
  fail("notice message is too large; the complete agent input must be below 65536 bytes")
end

local listing, list_error = exec("omo", "agent", "list")
if list_error ~= nil then
  fail("could not list agents")
end
local ceo = nil
for line in string.gmatch((listing or "") .. "\n", "([^\n]*)\n") do
  local name, role, state = string.match(line, "^%s*(%S+)%s+(%S+)%s+(%S+)%s+job=")
  if name ~= nil and role == "ceo" and state ~= "done" and state ~= "dead" then
    ceo = name
    break
  end
end
if ceo == nil then
  fail("no living CEO is available; notice was not typed")
end

local _, type_error = exec("omo", "type", ceo, block, "--key", "enter")
if type_error ~= nil then
  fail("could not type notice to CEO")
end
return "notice: typed to " .. ceo
