local data = event.data or {}
local settings = config or {}

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

local invocation = 'omo plugin trigger bugreport report -- body=<absolute-path> "<title>"'
local required_headings = {
  "## Summary",
  "## Observed behavior",
  "## Expected behavior",
  "## Steps or evidence",
  "## Anonymization check"
}

local function usage_guidance()
  return "Correct invocation: " .. invocation ..
    "\nRequired headings: " .. table.concat(required_headings, ", ")
end

local function usage_error(fault)
  local message = fault .. "\n" .. usage_guidance()
  local caller = trim(data.caller)
  if caller ~= "" and caller ~= "user" then
    exec("omo", "send", "-t", caller, "-s", "bugreport report usage", "-p", "normal", message)
  end
  fail(message)
end

local args = data.args or {}
local body_path = ""
local title = ""
local title_count = 0
for _, raw_arg in ipairs(args) do
  local arg = tostring(raw_arg or "")
  local trimmed = trim(arg)
  if trimmed == "" then
    usage_error("title must not be empty")
  end
  local key, value = string.match(arg, "^([%a_][%w_-]*)=(.*)$")
  if key ~= nil then
    value = trim(value)
    if key == "body" then
      if body_path ~= "" then
        usage_error("body= may be specified only once")
      end
      if value == "" then
        usage_error("body= must name an absolute Markdown file")
      end
      body_path = value
    else
      usage_error("unknown key-like argument " .. arg)
    end
  elseif trimmed == "body" then
    usage_error("body must use body=<absolute-path>")
  elseif string.find(arg, "=", 1, true) ~= nil then
    usage_error("unknown key-like argument " .. arg)
  else
    title_count = title_count + 1
    if title_count > 1 then
      usage_error("exactly one title argument is allowed")
    end
    title = trimmed
  end
end

if title_count == 0 then
  usage_error("exactly one non-empty title is required")
end
if body_path == "" then
  usage_error("body=<absolute-path> is required")
end

local function absolute_path(value)
  if string.sub(value, 1, 1) == "/" then
    return true
  end
  if string.sub(value, 1, 2) == "\\\\" then
    return true
  end
  return string.sub(value, 2, 2) == ":" and
    (string.sub(value, 3, 3) == "/" or string.sub(value, 3, 3) == "\\")
end

if not absolute_path(body_path) then
  usage_error("body path must be absolute; POSIX, Windows drive-root, and UNC paths are supported")
end

local function valid_utf8(value)
  local index = 1
  while index <= #value do
    local first = string.byte(value, index)
    local length = 1
    local minimum = 0
    local maximum = 0x10ffff
    if first <= 0x7f then
      length = 1
    elseif first >= 0xc2 and first <= 0xdf then
      length = 2
      minimum = 0x80
      maximum = 0x7ff
    elseif first >= 0xe0 and first <= 0xef then
      length = 3
      minimum = 0x800
      maximum = 0xffff
    elseif first >= 0xf0 and first <= 0xf4 then
      length = 4
      minimum = 0x10000
      maximum = 0x10ffff
    else
      return false
    end
    if length == 1 then
      index = index + 1
    else
      local codepoint = first
      if length == 2 then
        codepoint = first - 0xc0
      elseif length == 3 then
        codepoint = first - 0xe0
      else
        codepoint = first - 0xf0
      end
      for offset = 1, length - 1 do
        local next_byte = string.byte(value, index + offset)
        if next_byte == nil or next_byte < 0x80 or next_byte > 0xbf then
          return false
        end
        codepoint = codepoint * 0x40 + (next_byte - 0x80)
      end
      if codepoint < minimum or codepoint > maximum or (codepoint >= 0xd800 and codepoint <= 0xdfff) then
        return false
      end
      index = index + length
    end
  end
  return true
end

local input = io.open(body_path, "rb")
if input == nil then
  usage_error("report body could not be read: " .. body_path)
end
local read_ok, body = pcall(function()
  return input:read("*a")
end)
input:close()
if not read_ok or body == nil then
  usage_error("report body could not be read: " .. body_path)
end
if #body > 61440 then
  usage_error("report body exceeds 61440 bytes")
end
if not valid_utf8(body) then
  usage_error("report body is not valid UTF-8")
end
body = string.gsub(body, "%s+$", "")
if trim(body) == "" then
  usage_error("report body is empty after trimming")
end

local found_headings = {}
for line in string.gmatch(body .. "\n", "([^\n]*)\n") do
  if string.sub(line, -1) == "\r" then
    line = string.sub(line, 1, -2)
  end
  local heading = string.match(line, "^##[ \t]+(.+)$")
  if heading ~= nil then
    heading = trim(heading)
    heading = string.gsub(heading, "[ \t]+#+[ \t]*$", "")
    found_headings[string.lower(heading)] = true
  end
end
local missing = {}
for _, heading in ipairs(required_headings) do
  local name = string.sub(heading, 4)
  if not found_headings[string.lower(name)] then
    table.insert(missing, heading)
  end
end
if #missing > 0 then
  usage_error("missing required headings: " .. table.concat(missing, ", "))
end

local function path_join(left, right)
  local platform = omo.platform()
  local separator = platform.os == "windows" and "\\" or "/"
  left = string.gsub(left, "[\\/]$", "")
  right = string.gsub(right, "^[\\/]", "")
  return left .. separator .. right
end

local function report_directory()
  local configured = tostring(settings.local_dir or "")
  if trim(configured) ~= "" then
    if string.find(configured, "[\0\r\n]", 1) ~= nil then
      fail("local_dir must not contain NUL or newline characters")
    end
    return configured
  end
  local home = trim(os.getenv("OMO_HOME") or "")
  local platform = omo.platform().os
  if home == "" then
    if platform == "windows" then
      home = trim(os.getenv("APPDATA") or "")
      if home == "" then
        fail("APPDATA is required when OMO_HOME is not set on Windows")
      end
      return path_join(path_join(home, "omo"), "bugreports")
    end
    home = trim(os.getenv("HOME") or "")
    if home == "" then
      fail("HOME is required when OMO_HOME is not set")
    end
    return path_join(path_join(home, ".local"), path_join("omo", "bugreports"))
  end
  return path_join(home, "bugreports")
end

local function environment(mode)
  local version, version_error = exec("omo", "--version")
  if version_error ~= nil then
    fail("could not determine omo version")
  end
  version = trim(version)
  if version == "" then
    version = "unknown"
  end
  local platform = omo.platform()
  local os_name = trim(platform.os or "unknown")
  local arch = trim(platform.arch or "unknown")
  local caller_role = trim(data.caller_role)
  if caller_role == "" then
    caller_role = "unknown"
  end
  return "\n\n## Environment\n" ..
    "- omo version: " .. version .. "\n" ..
    "- OS/architecture: " .. os_name .. "/" .. arch .. "\n" ..
    "- mode: " .. mode .. "\n" ..
    "- caller_role: " .. caller_role .. "\n" ..
    "- plugin: bugreport\n"
end

local function slugify(value)
  local slug = string.lower(value)
  slug = string.gsub(slug, "[^%w]+", "-")
  slug = string.gsub(slug, "^-+", "")
  slug = string.gsub(slug, "-+$", "")
  slug = string.sub(slug, 1, 64)
  if slug == "" then
    return "bugreport"
  end
  return slug
end

local function write_local(finished_body)
  local directory = report_directory()
  local made, mkdir_error = omo.mkdir_all(directory)
  if not made then
    fail("could not create report directory")
  end
  local stamp = os.date("%Y%m%d-%H%M%S")
  local slug = slugify(title)
  for suffix = 1, 10000 do
    local suffix_text = suffix == 1 and "" or "-" .. tostring(suffix)
    local path = path_join(directory, stamp .. "-" .. slug .. suffix_text .. ".md")
    local written, write_error = omo.write_file_exclusive(path, finished_body)
    if written then
      return path
    end
    local existing = io.open(path, "rb")
    if existing ~= nil then
      existing:close()
    else
      fail("could not write report file")
    end
  end
  fail("could not choose a unique report filename")
end

local function notify(result, subject)
  exec("omo", "send", "-t", "user", "-s", subject, "-p", "normal", result)
  exec("omo", "send", "-t", "ceo", "-s", subject, "-p", "normal", result)
end

local function finish_local(context)
  local finished_body = body .. environment("local")
  local path = write_local(finished_body)
  local result = "file: " .. path
  omo.log(context .. ": " .. result)
  local subject = "bugreport report"
  if string.find(context, "fallback", 1, true) ~= nil then
    subject = "bugreport report (local fallback)"
  elseif string.find(context, "review required", 1, true) ~= nil then
    subject = "bugreport report (review required)"
  end
  notify(result, subject)
  return result
end

local mode = trim(settings.mode)
if mode == "" then
  mode = "github"
end
if mode ~= "local" and mode ~= "github" then
  fail("mode must be local or github")
end

local caller_role = trim(data.caller_role)
if settings.review_before_publish == true and caller_role ~= "user" and caller_role ~= "ceo" then
  return finish_local("review required; GitHub publication skipped")
end

if mode == "local" then
  return finish_local("local report")
end

local repository = trim(settings.repository)
if repository == "" then
  fail("repository is required for GitHub mode")
end
local auth_output, auth_error = exec("gh", "auth", "status")
if auth_error ~= nil then
  if settings.fallback_local ~= false then
    return finish_local("GitHub authentication failed; local fallback")
  end
  fail("GitHub authentication failed and local fallback is disabled")
end

local finished_body = body .. environment("github")
local gh_args = {"issue", "create", "-R", repository, "--title", title, "--body", finished_body}
local labels = settings.labels
if labels == nil then
  labels = {"bug", "omo-report"}
end
for _, label in ipairs(labels) do
  label = trim(label)
  if label ~= "" then
    table.insert(gh_args, "--label")
    table.insert(gh_args, label)
  end
end
local issue_output, issue_error = exec("gh", unpack(gh_args))
if issue_error ~= nil then
  if settings.fallback_local ~= false then
    return finish_local("GitHub issue creation failed; local fallback")
  end
  fail("GitHub issue creation failed and local fallback is disabled")
end
local issue_url = string.match(trim(issue_output), "https?://[^%s]+")
if issue_url == nil then
  if settings.fallback_local ~= false then
    return finish_local("GitHub issue creation returned no URL; local fallback")
  end
  fail("GitHub issue creation returned no URL and local fallback is disabled")
end
local result = "issue: " .. issue_url .. " (created)"
omo.log("GitHub issue created: " .. result)
notify(result, "bugreport report")
return result
