local data = event.data or {}
local settings = config or {}
local title = ""

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

local function path_join(left, right)
  local separator = omo.platform().os == "windows" and "\\" or "/"
  left = string.gsub(left, "[\\/]$", "")
  right = string.gsub(right, "^[\\/]", "")
  return left .. separator .. right
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

local function environment_path(name, required)
  local raw = tostring(os.getenv(name) or "")
  if raw == "" then
    if required then
      fail(name .. " is required when OMO_HOME is not set")
    end
    return ""
  end
  local value = trim(raw)
  if not absolute_path(value) then
    fail(name .. " must be an absolute path")
  end
  return value
end

local function report_directory()
  local configured = tostring(settings.local_dir or "")
  if trim(configured) ~= "" then
    if string.find(configured, "[\0\r\n]", 1) ~= nil then
      fail("local_dir must not contain NUL or newline characters")
    end
    return configured
  end
  local platform = omo.platform().os
  local raw_home = tostring(os.getenv("OMO_HOME") or "")
  if raw_home ~= "" then
    return path_join(environment_path("OMO_HOME", true), "bugreports")
  end
  if platform == "windows" then
    return path_join(path_join(environment_path("APPDATA", true), "omo"), "bugreports")
  end
  return path_join(path_join(environment_path("HOME", true), ".local"), path_join("omo", "bugreports"))
end

local stop_words = {
  a = true, an = true, ["and"] = true, are = true, as = true, at = true, be = true,
  by = true, ["for"] = true, from = true, ["in"] = true, is = true, it = true, of = true,
  on = true, ["or"] = true, that = true, the = true, this = true, to = true, was = true,
  were = true, with = true
}

local function significant_tokens(value)
  local tokens = {}
  for token in string.gmatch(string.lower(tostring(value or "")), "[%w]+") do
    if #token >= 3 and not stop_words[token] then
      tokens[token] = true
    end
  end
  return tokens
end

local function probable_overlap(query, candidate)
  local wanted = significant_tokens(query)
  local count = 0
  local matches = 0
  local available = significant_tokens(candidate)
  for token in pairs(wanted) do
    count = count + 1
    if available[token] then
      matches = matches + 1
    end
  end
  return count > 0 and matches / count >= 0.60
end

local function json_string(value)
  value = string.gsub(value or "", '\\\\', "\\")
  value = string.gsub(value, '\\"', '"')
  value = string.gsub(value, "\\n", "\n")
  return value
end

local function github_matches(query)
  local repository = trim(settings.repository)
  if repository == "" then
    return {}, "GitHub search unavailable: repository is not configured"
  end
  local output, search_error = exec("gh", "issue", "list", "-R", repository, "--state", "open", "--search", query, "--json", "number,title,url", "--limit", "20")
  if search_error ~= nil then
    return {}, "GitHub search unavailable: " .. trim(search_error)
  end
  local matches = {}
  for object in string.gmatch(output or "", "%b{}") do
    local number = string.match(object, '"number"%s*:%s*(%d+)')
    local issue_title = string.match(object, '"title"%s*:%s*"(.-)"')
    local url = string.match(object, '"url"%s*:%s*"(.-)"')
    issue_title = json_string(issue_title)
    url = json_string(url)
    if number ~= nil and issue_title ~= nil and probable_overlap(query, issue_title) then
      local label = "issue #" .. number .. ": " .. issue_title
      if url ~= "" then
        label = label .. " (" .. url .. ")"
      end
      table.insert(matches, label)
    end
  end
  return matches, nil
end

local function local_report_parts(contents)
  local title = ""
  local summary = {}
  local in_summary = false
  for line in string.gmatch(contents .. "\n", "([^\n]*)\n") do
    if string.sub(line, -1) == "\r" then
      line = string.sub(line, 1, -2)
    end
    if title == "" then
      title = string.match(line, "^#%s+(.+)$") or ""
    end
    local heading = string.match(line, "^##[ \t]+(.+)$")
    if heading ~= nil then
      in_summary = string.lower(trim(heading)) == "summary"
    elseif in_summary then
      table.insert(summary, line)
    end
  end
  return trim(title), table.concat(summary, "\n")
end

local function local_matches(query)
  local ok, directory = pcall(report_directory)
  if not ok then
    return {}, "Local search unavailable: " .. tostring(directory)
  end
  local files, list_error = omo.list_files(directory)
  if list_error ~= "" then
    if string.find(string.lower(list_error), "no such file", 1, true) ~= nil then
      return {}, nil
    end
    return {}, "Local search unavailable: " .. list_error
  end
  local matches = {}
  for _, name in ipairs(files) do
    local path = path_join(directory, name)
    local contents, read_error = omo.read_file(path)
    if read_error == "" then
      local title, summary = local_report_parts(contents)
      if probable_overlap(query, title .. "\n" .. summary) then
        table.insert(matches, "local report: " .. path)
      end
    end
  end
  return matches, nil
end

local function duplicate_search(query)
  local matches, github_note = github_matches(query)
  local local_hits, local_note = local_matches(query)
  for _, match in ipairs(local_hits) do
    table.insert(matches, match)
  end
  local notes = {}
  if github_note ~= nil then
    table.insert(notes, github_note)
  end
  if local_note ~= nil then
    table.insert(notes, local_note)
  end
  return matches, notes
end

local function search_result(query, matches, notes, duplicate_guidance)
  local lines = {}
  for _, note in ipairs(notes) do
    table.insert(lines, note)
  end
  if #matches == 0 then
    table.insert(lines, "No probable duplicates found for: " .. query)
  else
    table.insert(lines, "Probable duplicate(s) for: " .. query)
    for _, match in ipairs(matches) do
      table.insert(lines, "- " .. match)
    end
    if duplicate_guidance then
      table.insert(lines, "Add evidence to an existing item or rerun with force=true.")
    end
  end
  return table.concat(lines, "\n")
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
      length, minimum, maximum = 2, 0x80, 0x7ff
    elseif first >= 0xe0 and first <= 0xef then
      length, minimum, maximum = 3, 0x800, 0xffff
    elseif first >= 0xf0 and first <= 0xf4 then
      length, minimum, maximum = 4, 0x10000, 0x10ffff
    else
      return false
    end
    if length == 1 then
      index = index + 1
    else
      local codepoint = first - ({[2] = 0xc0, [3] = 0xe0, [4] = 0xf0})[length]
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

local function read_body(path, usage)
  local input = io.open(path, "rb")
  if input == nil then
    usage_error(usage .. " could not be read: " .. path)
  end
  local read_ok, contents = pcall(function() return input:read("*a") end)
  input:close()
  if not read_ok or contents == nil then
    usage_error(usage .. " could not be read: " .. path)
  end
  return contents
end

local function environment(mode)
  local version, version_error = exec("omo", "--version")
  version = trim(version)
  if version_error ~= nil or version == "" then
    omo.log("omo version lookup failed; using unknown")
    version = "unknown"
  end
  local platform = omo.platform()
  local caller_role = trim(data.caller_role)
  if caller_role == "" then
    caller_role = "unknown"
  end
  return "\n\n## Environment\n" ..
    "- omo version: " .. version .. "\n" ..
    "- OS/architecture: " .. trim(platform.os or "unknown") .. "/" .. trim(platform.arch or "unknown") .. "\n" ..
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
  return slug == "" and "bugreport" or slug
end

local function write_local(finished_body)
  local directory = report_directory()
  local made = omo.mkdir_all(directory)
  if not made then
    fail("could not create report directory")
  end
  local stamp = os.date("%Y%m%d-%H%M%S")
  local slug = slugify(title)
  for suffix = 1, 10000 do
    local suffix_text = suffix == 1 and "" or "-" .. tostring(suffix)
    local path = path_join(directory, stamp .. "-" .. slug .. suffix_text .. ".md")
    local written = omo.write_file_exclusive(path, finished_body)
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

local function send(target, subject, message)
  local _, send_error = exec("omo", "send", "-t", target, "-s", subject, "-p", "normal", message)
  return send_error
end

local function notify(result, subject, local_path)
  local caller_role = trim(data.caller_role)
  local failures = {}
  if local_path ~= nil and caller_role ~= "user" and caller_role ~= "ceo" then
    local message = "A local bugreport was written at " .. local_path .. ". It was not published. ask the user whether to publish it, then run `omo plugin trigger bugreport publish -- " .. local_path .. "`."
    if send("ceo", subject, message) ~= nil then
      table.insert(failures, "ceo")
    end
  else
    if send("user", subject, result) ~= nil then
      table.insert(failures, "user")
    end
    if send("ceo", subject, result) ~= nil then
      table.insert(failures, "ceo")
    end
  end
  if #failures > 0 then
    local message = "mandatory notification failed for " .. table.concat(failures, ", ")
    omo.log(message .. "; report result: " .. result)
    fail(message .. "; report result: " .. result)
  end
end

local function finish_local(context, report_body)
  local path = write_local(report_body)
  local result = "file: " .. path .. "; report written at " .. path .. "; not published; ask the user before running `omo plugin trigger bugreport publish -- " .. path .. "`"
  omo.log(context .. ": " .. result)
  notify(result, "bugreport report", path)
  return result
end

local function issue_url(output)
  return string.match(trim(output), "https?://[^%s]+")
end

local function create_issue(title_value, report_body)
  local repository = trim(settings.repository)
  if repository == "" then
    fail("repository is required for GitHub publishing")
  end
  local body_file = os.tmpname()
  local body_written = omo.write_file_exclusive(body_file, report_body)
  if not body_written then
    fail("could not create temporary GitHub body file")
  end
  local args = {"issue", "create", "-R", repository, "--title", title_value, "--body-file", body_file}
  local labels = settings.labels or {"bug", "omo-report"}
  for _, label in ipairs(labels) do
    label = trim(label)
    if label ~= "" then
      table.insert(args, "--label")
      table.insert(args, label)
    end
  end
  local output, create_error = exec("gh", unpack(args))
  os.remove(body_file)
  if create_error ~= nil then
    return nil, "GitHub issue creation failed: " .. trim(create_error)
  end
  local url = issue_url(output)
  if url == nil then
    return nil, "GitHub issue creation returned no URL"
  end
  return url, nil
end

local action = trim(data.action)
if action == "search" then
  local args = data.args or {}
  local query = trim(table.concat(args, " "))
  if query == "" then
    fail("search query must not be empty")
  end
  local matches, notes = duplicate_search(query)
  return search_result(query, matches, notes, false)
end

if action == "publish" then
  local args = data.args or {}
  if #args ~= 1 or trim(args[1]) == "" then
    fail("publish requires exactly one absolute local report path")
  end
  local report_path = trim(args[1])
  if not absolute_path(report_path) then
    fail("publish report path must be absolute")
  end
  local directory = report_directory()
  local inside, path_error = omo.path_is_within(directory, report_path)
  if path_error ~= "" or not inside then
    fail("publish report path must be inside the configured bugreport directory")
  end
  local report_body = read_body(report_path, "publish report")
  local published = string.match(report_body, "\nPublished:%s*(https?://[^%s]+)")
  if published ~= nil then
    fail("report was already published: " .. published)
  end
  local report_title = local_report_parts(report_body)
  if report_title == "" then
    fail("local report must begin with a # title heading")
  end
  local github_hits, github_note = github_matches(report_title)
  if #github_hits > 0 then
    return search_result(report_title, github_hits, github_note and {github_note} or {}, true)
  end
  local url, create_error = create_issue(report_title, report_body)
  if create_error ~= nil then
    fail(create_error .. "; local report was preserved at " .. report_path)
  end
  local output = io.open(report_path, "ab")
  if output == nil then
    fail("issue created at " .. url .. ", but local report could not be updated")
  end
  output:write("\nPublished: " .. url .. "\n")
  output:close()
  local result = "issue: " .. url .. " (created)"
  omo.log("GitHub issue created: " .. result)
  notify(result, "bugreport publish", nil)
  return result
end

local args = data.args or {}
local body_path = ""
title = ""
local title_count = 0
local force = false
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
    elseif key == "force" then
      if value ~= "true" and value ~= "false" then
        usage_error("force= must be true or false")
      end
      force = value == "true"
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
if string.find(title, "[\r\n]", 1) ~= nil then
  usage_error("title must not contain newlines")
end
if body_path == "" then
  usage_error("body=<absolute-path> is required")
end
if not absolute_path(body_path) then
  usage_error("body path must be absolute; POSIX, Windows drive-root, and UNC paths are supported")
end

local body = read_body(body_path, "report body")
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
    heading = trim(string.gsub(heading, "[ \t]+#+[ \t]*$", ""))
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

local matches, notes = duplicate_search(title)
if #matches > 0 and not force then
  return search_result(title, matches, notes, true)
end

local mode = trim(settings.mode)
if mode == "" then
  mode = "local"
end
if mode ~= "local" and mode ~= "github" then
  fail("mode must be local or github")
end
local auto_publish = settings.auto_publish == true
local finished_body
if mode == "local" or not auto_publish then
  finished_body = "# " .. title .. "\n\n" .. body .. environment("local")
  return finish_local("local report", finished_body)
end

local repository = trim(settings.repository)
if repository == "" then
  fail("repository is required for GitHub mode")
end
local _, auth_error = exec("gh", "auth", "status")
if auth_error ~= nil then
  if settings.fallback_local ~= false then
    finished_body = "# " .. title .. "\n\n" .. body .. environment("local")
    return finish_local("GitHub authentication failed; local fallback", finished_body)
  end
  fail("GitHub authentication failed and local fallback is disabled")
end
finished_body = "# " .. title .. "\n\n" .. body .. environment("github")
local url, create_error = create_issue(title, finished_body)
if create_error ~= nil then
  if settings.fallback_local ~= false then
    local local_body = "# " .. title .. "\n\n" .. body .. environment("local")
    return finish_local("GitHub issue creation failed; local fallback", local_body)
  end
  fail("GitHub issue creation failed and local fallback is disabled")
end
local result = "issue: " .. url .. " (created)"
omo.log("GitHub issue created: " .. result)
notify(result, "bugreport report", nil)
return result
