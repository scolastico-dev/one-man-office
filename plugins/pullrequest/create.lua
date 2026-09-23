local data = event.data or {}
local settings = config or {}

local function trim(value)
  return string.gsub(tostring(value or ""), "^%s*(.-)%s*$", "%1")
end

local secret_values = {}

local function remember_secret(value)
  value = trim(value)
  if value ~= "" then
    table.insert(secret_values, value)
  end
end

local function escape_pattern(value)
  return string.gsub(value, "([%(%)%.%%%+%-%*%?%[%^%$])", "%%%1")
end

local function redact(value)
  value = tostring(value or "")
  for _, secret in ipairs(secret_values) do
    value = string.gsub(value, escape_pattern(secret), "<redacted>")
  end
  value = string.gsub(value, "([%a][%w+.-]*://)[^%s/@]+:[^%s/@]+@", "%1<redacted>@")
  value = string.gsub(value, "([%a][%w+.-]*://)[^%s/@]+@", "%1<redacted>@")
  value = string.gsub(value, "([Aa][Uu][Tt][Hh][Oo][Rr][Ii][Zz][Aa][Tt][Ii][Oo][Nn]%s*:%s*)[^\r\n]*", "%1<redacted>")
  value = string.gsub(value, "([Tt]oken%s*[:=]%s*)[^%s]+", "%1<redacted>")
  return value
end

remember_secret(settings.token)

local function fail(message)
  error("pullrequest: " .. message)
end

local function exec(...)
  local output, exec_error = omo.exec(...)
  if exec_error ~= "" then
    return nil, exec_error, output
  end
  return output, nil, nil
end

local function command_argument(value)
  value = tostring(value or "")
  if string.match(value, "^[%w%._/@:+%%-]+$") then
    return value
  end
  return "'" .. string.gsub(value, "'", "'\\''") .. "'"
end

local function command_text(...)
  local parts = {}
  for index = 1, select("#", ...) do
    table.insert(parts, command_argument(select(index, ...)))
  end
  return redact(table.concat(parts, " "))
end

local function command_failure(output, exec_error, ...)
  local detail = redact(trim(output))
  if detail == "" then
    detail = redact(trim(exec_error))
  elseif trim(exec_error) ~= "" then
    detail = detail .. " (" .. redact(trim(exec_error)) .. ")"
  end
  if detail == "" then
    detail = "command returned an error"
  end
  return command_text(...) .. " failed: " .. detail
end

local usage_invocation = 'omo plugin trigger pullrequest create -- [repo=<key>] body=<absolute-path> "<title>"'
local usage_guidance = "Correct invocation: " .. usage_invocation ..
  "\nRequired sections: ## Summary, ## What changed, ## Why, ## How it was verified" ..
  "\nRecommended sections: ## Risks and follow-ups, ## Jobs" ..
  "\nSee plugins/pullrequest/README.md description-file section"

local function usage_error(fault)
  local message = fault .. "\n" .. usage_guidance
  local caller = trim(data.caller)
  if caller ~= "" and caller ~= "user" then
    -- Guidance delivery is best effort. The synchronous error remains the
    -- source of truth when the caller's mail route is unavailable.
    exec("omo", "send", "-t", caller, "-s", "pullrequest create usage", "-p", "normal", message)
  end
  fail(message)
end

local function require_text(name)
  local value = trim(data[name])
  if value == "" then
    fail("job metadata is missing " .. name)
  end
  return value
end

local args = data.args or {}
local selected_repo = ""
local body_path = ""
local requested_title = ""
local title_count = 0
for _, raw_arg in ipairs(args) do
  local arg = trim(raw_arg)
  if arg == "" then
    usage_error("title must not be empty")
  end
  local key, value = string.match(arg, "^([%a_][%w_-]*)=(.*)$")
  if key ~= nil then
    value = trim(value)
    if key == "repo" then
      if selected_repo ~= "" then
        usage_error("repo= may be specified only once")
      end
      if value == "" then
        usage_error("repo= must name a repository")
      end
      selected_repo = value
    elseif key == "body" then
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
  elseif arg == "repo" or arg == "body" then
    usage_error(arg .. " must use " .. arg .. "=" .. (arg == "body" and "<absolute-path>" or "<key>"))
  elseif string.find(arg, "=", 1, true) ~= nil then
    usage_error("unknown key-like argument " .. arg)
  else
    title_count = title_count + 1
    if title_count > 1 then
      usage_error("create accepts at most one title argument")
    end
    requested_title = arg
  end
end

if body_path == "" then
  usage_error("body=<absolute-path> is required")
end

if requested_title ~= "" then
  local kind, suffix = string.match(requested_title, "^(%a+)(.*)$")
  local valid_kinds = {
    feat = true, fix = true, docs = true, test = true, refactor = true,
    perf = true, chore = true, build = true, ci = true, style = true,
    revert = true, merge = true
  }
  if not valid_kinds[kind] then
    usage_error("title must be a scoped Conventional Commits subject, for example fix(company): center sidebar resizer")
  end
  if string.sub(suffix, 1, 1) == "(" then
    local scope, rest = string.match(suffix, "^%(([^%)]+)%)(.*)$")
    if scope == nil then
      usage_error("title must be a scoped Conventional Commits subject, for example fix(company): center sidebar resizer")
    end
    suffix = rest
  end
  if string.sub(suffix, 1, 1) == "!" then
    suffix = string.sub(suffix, 2)
  end
  if not string.match(suffix, "^: .+$") then
    usage_error("title must be a scoped Conventional Commits subject, for example fix(company): center sidebar resizer")
  end
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

local description_file = io.open(body_path, "rb")
if description_file == nil then
  usage_error("description file could not be read: " .. body_path)
end
local read_ok, body = pcall(function()
  return description_file:read("*a")
end)
description_file:close()
if not read_ok or body == nil then
  usage_error("description file could not be read: " .. body_path)
end
if #body > 61440 then
  usage_error("description file exceeds 61440 bytes")
end
if not valid_utf8(body) then
  usage_error("description file is not valid UTF-8")
end
body = string.gsub(body, "%s+$", "")
if trim(body) == "" then
  usage_error("description file is empty after trimming")
end

local required_headings = {"Summary", "What changed", "Why", "How it was verified"}
local found_headings = {}
local in_jobs = false
for line in string.gmatch(body .. "\n", "([^\n]*)\n") do
  if string.sub(line, -1) == "\r" then
    line = string.sub(line, 1, -2)
  end
  local heading = string.match(line, "^##[ \t]+(.+)$")
  if heading ~= nil then
    heading = trim(heading)
    heading = string.gsub(heading, "[ \t]+#+[ \t]*$", "")
    heading = string.lower(heading)
    found_headings[heading] = true
    in_jobs = heading == "jobs"
  elseif string.match(line, "^#[ \t]+") then
    in_jobs = false
  elseif in_jobs then
    local job_reference = string.match(line, "#%d+")
    if job_reference ~= nil then
      local number = string.sub(job_reference, 2)
      usage_error("## Jobs must use job " .. number .. " or " .. number .. ", not " .. job_reference .. "; GitHub would mis-link it as a PR or issue")
    end
  end
end
local missing_headings = {}
for _, heading in ipairs(required_headings) do
  if not found_headings[string.lower(heading)] then
    table.insert(missing_headings, "## " .. heading)
  end
end
if #missing_headings > 0 then
  usage_error("missing required headings: " .. table.concat(missing_headings, ", "))
end

local job_id = trim(data.job_id)
if job_id == "" or job_id == "0" then
  fail("job metadata is required")
end

local entries = data.integration_branches
if entries == nil then
  entries = {{
    repo = require_text("repo"),
    branch = require_text("branch"),
    base_branch = require_text("base_branch"),
    worktree = require_text("worktree")
  }}
end

local selected_entries = {}
local valid_repos = {}
for _, entry in ipairs(entries) do
  local entry_repo = trim(entry.repo)
  if entry_repo ~= "" then
    table.insert(valid_repos, entry_repo)
    if selected_repo == "" or selected_repo == entry_repo then
      table.insert(selected_entries, entry)
    end
  end
end
if selected_repo ~= "" and #selected_entries == 0 then
	if #valid_repos == 0 then
		usage_error("unknown repository " .. selected_repo .. "; no as-is integration branches are available (effective repository policy must be asis)")
	end
  usage_error("unknown repository " .. selected_repo .. "; valid keys: " .. table.concat(valid_repos, ", "))
end
if #selected_entries == 0 then
	if data.integration_branches ~= nil then
		fail("no as-is integration branches are available (effective repository policy must be asis)")
	end
	fail("integration branch metadata is missing")
end

local function create_one(entry, requested_title)
local worktree = trim(entry.worktree)
local branch = trim(entry.branch)
local base_branch = trim(entry.base_branch)
local repo = trim(entry.repo)
if worktree == "" or branch == "" or base_branch == "" or repo == "" then
  fail("integration branch metadata is incomplete for " .. (repo ~= "" and repo or "unknown repository"))
end

local remote_name = trim(settings.remote)
if remote_name == "" then
  remote_name = "origin"
end
if string.find(remote_name, "[%z\r\n%s]") then
  fail("remote is invalid")
end

local remote_url, remote_error, remote_output = exec("git", "-C", worktree, "remote", "get-url", remote_name)
if remote_error ~= nil then
  fail("could not read the configured Git remote: " .. command_failure(remote_output, remote_error, "git", "-C", worktree, "remote", "get-url", remote_name))
end
remote_url = trim(remote_url)

local actual_branch, branch_error, branch_output = exec("git", "-C", worktree, "branch", "--show-current")
if branch_error ~= nil then
  fail("could not read the current Git branch: " .. command_failure(branch_output, branch_error, "git", "-C", worktree, "branch", "--show-current"))
end
actual_branch = trim(actual_branch)

local actual_head_sha, head_error, head_output = exec("git", "-C", worktree, "rev-parse", "HEAD")
if head_error ~= nil then
  fail("could not read the current Git HEAD: " .. command_failure(head_output, head_error, "git", "-C", worktree, "rev-parse", "HEAD"))
end
actual_head_sha = trim(actual_head_sha)

local integration_sha, integration_error, integration_output = exec("git", "-C", worktree, "rev-parse", branch)
if integration_error ~= nil then
  fail("could not read integration branch " .. branch .. ": " .. command_failure(integration_output, integration_error, "git", "-C", worktree, "rev-parse", branch))
end
integration_sha = trim(integration_sha)

local commit_count, count_error, count_output = exec("git", "-C", worktree, "rev-list", "--count", base_branch .. ".." .. branch)
if count_error ~= nil then
  fail("could not inspect integration branch " .. branch .. ": " .. command_failure(count_output, count_error, "git", "-C", worktree, "rev-list", "--count", base_branch .. ".." .. branch))
end
if trim(commit_count) == "0" then
  return {repo = repo, state = "no_change", branch = branch, base_branch = base_branch, title = requested_title ~= "" and requested_title or "Changes from " .. branch}
end

local function parse_remote(value)
  local scheme, rest = string.match(value, "^(%a[%w+.-]*)://(.+)$")
  local host
  local path
  if scheme ~= nil then
    local slash = string.find(rest, "/", 1, true)
    if slash == nil then
      return nil
    end
    local authority = string.sub(rest, 1, slash - 1)
    host = string.gsub(authority, "^.*@", "")
    host = string.gsub(host, ":%d+$", "")
    path = string.sub(rest, slash + 1)
  else
    host, path = string.match(value, "^[^@]+@([^:]+):(.+)$")
  end
  if host == nil or path == nil then
    return nil
  end
  path = string.gsub(path, "^/+", "")
  path = string.gsub(path, "/+$", "")
  path = string.gsub(path, "%.git$", "")
  local slash = string.find(path, "/", 1, true)
  if slash == nil then
    return nil
  end
  local owner = string.sub(path, 1, slash - 1)
  local project = string.sub(path, slash + 1)
  if owner == "" or project == "" then
    return nil
  end
  return {host = string.lower(host), path = path, owner = owner, project = project}
end

local remote = parse_remote(remote_url)
if remote == nil then
  fail("Git remote must be an HTTPS, SSH, or scp-style URL")
end

local function encode(value)
  local output = {}
  for index = 1, #value do
    local byte = string.byte(value, index)
    if (byte >= 48 and byte <= 57) or (byte >= 65 and byte <= 90) or (byte >= 97 and byte <= 122) or byte == 45 or byte == 46 or byte == 95 or byte == 126 then
      table.insert(output, string.char(byte))
    else
      table.insert(output, string.format("%%%02X", byte))
    end
  end
  return table.concat(output)
end

local function api_root(value, suffix)
  local root = string.gsub(trim(value), "/+$", "")
  if root == "" then
    return ""
  end
  if string.sub(root, -#suffix) == suffix then
    return root
  end
  return root .. suffix
end

local function json_string_at(body, start)
  if string.byte(body, start) ~= 34 then
    return nil
  end
  local output = {}
  local escaped = false
  for index = start + 1, #body do
    local byte = string.byte(body, index)
    if escaped then
      if byte == 110 then
        table.insert(output, "\n")
      elseif byte == 114 then
        table.insert(output, "\r")
      elseif byte == 116 then
        table.insert(output, "\t")
      else
        table.insert(output, string.char(byte))
      end
      escaped = false
    elseif byte == 92 then
      escaped = true
    elseif byte == 34 then
      return table.concat(output)
    else
      table.insert(output, string.char(byte))
    end
  end
  return nil
end

local function json_field_string(body, field)
  local key = '"' .. field .. '"'
  local depth = 0
  local in_string = false
  local escaped = false
  local index = 1
  while index <= #(body or "") do
    local byte = string.byte(body, index)
    if in_string then
      if escaped then
        escaped = false
      elseif byte == 92 then
        escaped = true
      elseif byte == 34 then
        in_string = false
      end
    elseif byte == 123 then
      depth = depth + 1
    elseif byte == 125 and depth > 0 then
      depth = depth - 1
    elseif byte == 34 then
      if depth == 1 and string.sub(body, index, index + #key - 1) == key then
        local cursor = index + #key
        while cursor <= #body and string.match(string.sub(body, cursor, cursor), "%s") ~= nil do
          cursor = cursor + 1
        end
        if string.byte(body, cursor) == 58 then
          cursor = cursor + 1
          while cursor <= #body and string.match(string.sub(body, cursor, cursor), "%s") ~= nil do
            cursor = cursor + 1
          end
          return json_string_at(body, cursor)
        end
      end
      in_string = true
    end
    index = index + 1
  end
  return nil
end

local function response_url(body)
  for _, key in ipairs({"html_url", "web_url", "url"}) do
    local value = json_field_string(body, key)
    if value ~= nil and string.match(value, "^https?://") ~= nil then
      return value
    end
  end
  return nil
end

local function cli_url(output)
  local value = string.match(output or "", "https?://[^%s\"']+")
  if value == nil then
    return nil
  end
  while string.match(value, "[\"'%,%]%}]$") do
    value = string.sub(value, 1, #value - 1)
  end
  return value
end

local function response_number(body, url)
  local value = string.match(body or "", '"number"%s*:%s*(%d+)')
  if value == nil then
    value = string.match(body or "", '"iid"%s*:%s*(%d+)')
  end
  if value ~= nil then
    return value
  end
  return string.match(url or "", "/(%d+)[/?]?$")
end

local function response_request(body)
  local url = response_url(body)
  return url, response_number(body, url)
end

local function json_objects(body)
  local objects = {}
  local start
  local depth = 0
  local in_string = false
  local escaped = false
  for index = 1, #(body or "") do
    local byte = string.byte(body, index)
    if in_string then
      if escaped then
        escaped = false
      elseif byte == 92 then
        escaped = true
      elseif byte == 34 then
        in_string = false
      end
    elseif byte == 34 then
      in_string = true
    elseif byte == 123 then
      if depth == 0 then
        start = index
      end
      depth = depth + 1
    elseif byte == 125 and depth > 0 then
      depth = depth - 1
      if depth == 0 and start ~= nil then
        table.insert(objects, string.sub(body, start, index))
        start = nil
      end
    end
  end
  return objects
end

local function json_object_at(body, start)
  if string.byte(body, start) ~= 123 then
    return nil
  end
  local depth = 0
  local in_string = false
  local escaped = false
  for index = start, #body do
    local byte = string.byte(body, index)
    if in_string then
      if escaped then
        escaped = false
      elseif byte == 92 then
        escaped = true
      elseif byte == 34 then
        in_string = false
      end
    elseif byte == 34 then
      in_string = true
    elseif byte == 123 then
      depth = depth + 1
    elseif byte == 125 then
      depth = depth - 1
      if depth == 0 then
        return string.sub(body, start, index)
      end
    end
  end
  return nil
end

local function json_field_object(body, field)
  local key = '"' .. field .. '"'
  local depth = 0
  local in_string = false
  local escaped = false
  local index = 1
  while index <= #(body or "") do
    local byte = string.byte(body, index)
    if in_string then
      if escaped then
        escaped = false
      elseif byte == 92 then
        escaped = true
      elseif byte == 34 then
        in_string = false
      end
    elseif byte == 123 then
      depth = depth + 1
    elseif byte == 125 and depth > 0 then
      depth = depth - 1
    elseif byte == 34 then
      if depth == 1 and string.sub(body, index, index + #key - 1) == key then
        local cursor = index + #key
        while cursor <= #body and string.match(string.sub(body, cursor, cursor), "%s") ~= nil do
          cursor = cursor + 1
        end
        if string.byte(body, cursor) == 58 then
          cursor = cursor + 1
          while cursor <= #body and string.match(string.sub(body, cursor, cursor), "%s") ~= nil do
            cursor = cursor + 1
          end
          if string.byte(body, cursor) == 123 then
            return json_object_at(body, cursor)
          end
        end
      end
      in_string = true
    end
    index = index + 1
  end
  return nil
end

local function response_requests(body)
  local requests = {}
  for _, value in ipairs(json_objects(body)) do
    local url = response_url(value)
    if url ~= nil then
      local head = json_field_object(value, "head") or ""
      local base = json_field_object(value, "base") or ""
      local number = string.match(url, "/(%d+)[/?]?$") or response_number(value, url)
      table.insert(requests, {
        url = url,
        number = number,
        head_sha = string.match(head, '"sha"%s*:%s*"([^"]+)"'),
        head_ref = string.match(head, '"ref"%s*:%s*"([^"]+)"'),
        base_ref = string.match(base, '"ref"%s*:%s*"([^"]+)"')
      })
    end
  end
  if #requests == 0 then
    local url, number = response_request(body)
    if url ~= nil then
      table.insert(requests, {url = url, number = number})
    end
  end
  return requests
end

local function find_response_request(body, head_sha, branch, base_branch, branch_filtered)
  local first
  for _, request in ipairs(response_requests(body)) do
    if first == nil then
      first = request
    end
    if branch_filtered and request.head_ref == branch and (request.base_ref == nil or request.base_ref == base_branch) then
      return request, false
    end
    if head_sha ~= nil and request.head_sha == head_sha then
      return request, request.head_ref ~= branch or request.base_ref ~= base_branch
    end
  end
  if branch_filtered and first ~= nil and first.head_sha == nil and first.head_ref == nil then
    return first, false
  end
  return nil, false
end

local function request(method, url, headers, fields)
  local options = {method = method, url = url, headers = headers or {}}
  if fields ~= nil then
    options.json = fields
  end
  local response, transport_error = omo.http(options)
  if response == nil then
    fail("provider request failed")
  end
  return response
end

local function request_form(method, url, headers, fields)
  local response, transport_error = omo.http({method = method, url = url, headers = headers or {}, form = fields})
  if response == nil then
    fail("provider request failed")
  end
  return response
end

local function success(response)
  return response.status >= 200 and response.status < 300
end

local function find_open_request_by_sha(root, path, headers, provider)
  local page = 1
  while true do
    local response = request("GET", root .. path .. "?state=open&per_page=100&page=" .. page, headers)
    if not success(response) then
      fail(provider .. " pull request SHA lookup failed")
    end
    local existing, different_branch = find_response_request(response.body, integration_sha, branch, base_branch, false)
    if existing ~= nil then
      return existing, different_branch
    end
    if #response_requests(response.body) < 100 then
      return nil, false
    end
    page = page + 1
  end
end

local function resolve_token()
  local configured = trim(settings.token)
  if configured ~= "" then
    remember_secret(configured)
    return configured
  end
  local name = trim(settings.token_env)
  if name == "" then
    return ""
  end
  if string.match(name, "^[A-Za-z_][A-Za-z0-9_]*$") == nil then
    fail("token_env is invalid")
  end
  local output, exec_error = exec("printenv", name)
  if exec_error == nil and trim(output) ~= "" then
    local value = trim(output)
    remember_secret(value)
    return value
  end
  output, exec_error = exec("cmd.exe", "/C", "set", name)
  if exec_error == nil then
    local value = string.match(output or "", "^" .. name .. "=(.-)\r?\n?$")
    if value ~= nil and trim(value) ~= "" then
      value = trim(value)
      remember_secret(value)
      return value
    end
  end
  return ""
end

local title = requested_title
if title == "" then
  title = "Changes from " .. branch
end

local function push_branch()
  local refspec = branch .. ":" .. branch
  local _, push_error, push_output = exec("git", "-C", worktree, "push", "-u", remote_name, refspec)
  if push_error ~= nil then
    fail("Git push failed for current branch " .. (actual_branch ~= "" and actual_branch or "(detached HEAD)") .. " (HEAD " .. actual_head_sha .. ") to integration branch " .. branch .. " (" .. integration_sha .. "): " .. command_failure(push_output, push_error, "git", "-C", worktree, "push", "-u", remote_name, refspec))
  end
end

local forge = string.lower(trim(settings.forge))
if forge == "" then
  forge = "auto"
end
if forge ~= "auto" and forge ~= "github" and forge ~= "forgejo" and forge ~= "gitea" and forge ~= "gitlab" then
  fail("forge must be auto, github, forgejo, gitea, or gitlab")
end

local function is_gitlab_host(host)
  if host == "gitlab.com" then
    return true
  end
  for _, configured_host in ipairs(settings.gitlab_hosts or {}) do
    if string.lower(trim(configured_host)) == host then
      return true
    end
  end
  return false
end

local function cli_requests(output)
  local requests = {}
  for object in string.gmatch(output or "", "{(.-)}") do
    local value = "{" .. object .. "}"
    local url = cli_url(value)
    if url ~= nil then
      table.insert(requests, {
        url = url,
        number = response_number(value, url),
        head_sha = string.match(value, '"headRefOid"%s*:%s*"([^"]+)"'),
        head_ref = string.match(value, '"headRefName"%s*:%s*"([^"]+)"'),
        base_ref = string.match(value, '"baseRefName"%s*:%s*"([^"]+)"')
      })
    end
  end
  return requests
end

local function find_cli_request(output, head_sha, branch, base_branch, branch_filtered)
  for _, request in ipairs(cli_requests(output)) do
    if branch_filtered and request.head_ref == branch and (request.base_ref == nil or request.base_ref == base_branch) then
      return request, false
    end
    if branch_filtered and request.head_ref == nil and request.head_sha == nil then
      return request, false
    end
    if request.head_sha == head_sha then
      return request, request.head_ref ~= nil and request.head_ref ~= branch
    end
  end
  return nil, false
end

if forge == "auto" then
  if remote.host == "github.com" then
    forge = "github"
  elseif is_gitlab_host(remote.host) then
    forge = "gitlab"
  else
    local probe_root = trim(settings.api_url)
    if probe_root == "" then
      probe_root = "https://" .. remote.host
    end
    local probe = request("GET", api_root(probe_root, "/api/v1") .. "/version")
    if success(probe) then
      forge = "forgejo"
    else
      fail("could not detect the Git forge")
    end
  end
end

local function github()
  local repo_path = remote.owner .. "/" .. remote.project
  local _, auth_error = exec("gh", "auth", "status")
  if auth_error == nil then
    local list_command = {"gh", "pr", "list", "--repo", repo_path, "--head", branch, "--base", base_branch, "--state", "open", "--json", "url,number,headRefOid,headRefName,baseRefName", "--limit", "100"}
    local list_output, list_error, list_stderr = exec(unpack(list_command))
    if list_error ~= nil then
      fail("GitHub CLI lookup failed: " .. command_failure(list_stderr, list_error, unpack(list_command)))
    end
    local existing, different_branch = find_cli_request(list_output, integration_sha, branch, base_branch, true)
    if existing == nil then
      local all_command = {"gh", "api", "--paginate", "repos/" .. repo_path .. "/pulls", "--method", "GET", "-f", "state=open", "-f", "per_page=100"}
      local all_output, all_error, all_stderr = exec(unpack(all_command))
      if all_error ~= nil then
        fail("GitHub CLI SHA lookup failed: " .. command_failure(all_stderr, all_error, unpack(all_command)))
      end
      existing, different_branch = find_response_request(all_output, integration_sha, branch, base_branch, false)
    end
    if existing ~= nil then
      if different_branch then
        return {url = existing.url, state = "existing"}
      end
      push_branch()
      local _, edit_error
      if requested_title == "" then
        local _, error, output = exec("gh", "pr", "edit", existing.url, "--body", body)
        edit_error = error
        if edit_error ~= nil then
          fail("GitHub CLI pull request update failed: " .. command_failure(output, edit_error, "gh", "pr", "edit", existing.url, "--body", "<description>"))
        end
      else
        local _, error, output = exec("gh", "pr", "edit", existing.url, "--body", body, "--title", requested_title)
        edit_error = error
        if edit_error ~= nil then
          fail("GitHub CLI pull request update failed: " .. command_failure(output, edit_error, "gh", "pr", "edit", existing.url, "--body", "<description>", "--title", requested_title))
        end
      end
      return {url = existing.url, state = "updated"}
    end
    push_branch()
    local created, create_error, create_output = exec("gh", "pr", "create", "--repo", repo_path, "--head", branch, "--base", base_branch, "--title", title, "--body", body)
    if create_error ~= nil then
      fail("GitHub CLI creation failed: " .. command_failure(create_output, create_error, "gh", "pr", "create", "--repo", repo_path, "--head", branch, "--base", base_branch, "--title", title, "--body", "<description>"))
    end
    local created_url = cli_url(created)
    if created_url == nil then
      fail("GitHub CLI returned no pull request URL")
    end
    return {url = created_url, state = "created"}
  end
  local token = resolve_token()
  if token == "" then
    fail("GitHub token is not configured")
  end
  local root = trim(settings.api_url)
  if root == "" then
    root = "https://api.github.com"
  end
  local headers = {Authorization = "Bearer " .. token, ["Accept"] = "application/vnd.github+json"}
  local path = "/repos/" .. encode(remote.owner) .. "/" .. encode(remote.project) .. "/pulls"
  local list = request("GET", root .. path .. "?state=open&head=" .. encode(remote.owner .. ":" .. branch) .. "&base=" .. encode(base_branch), headers)
  if not success(list) then
    fail("GitHub pull request lookup failed")
  end
  local existing_request, different_branch = find_response_request(list.body, integration_sha, branch, base_branch, true)
  if existing_request == nil then
    existing_request, different_branch = find_open_request_by_sha(root, path, headers, "GitHub")
  end
  local existing = existing_request and existing_request.url or nil
  local existing_number = existing_request and existing_request.number or nil
  if existing ~= nil then
    if existing_number == nil then
      fail("GitHub pull request lookup returned no request number")
    end
    if different_branch then
      return {url = existing, state = "existing"}
    end
    push_branch()
    local fields = {body = body}
    if requested_title ~= "" then
      fields.title = requested_title
    end
    local updated = request("PATCH", root .. path .. "/" .. existing_number, headers, fields)
    if not success(updated) then
      fail("GitHub pull request update failed")
    end
    return {url = existing, state = "updated"}
  end
  push_branch()
  local created = request("POST", root .. path, headers, {title = title, head = branch, base = base_branch, body = body})
  if not success(created) then
    fail("GitHub pull request creation failed")
  end
  local created_url = response_url(created.body)
  if created_url == nil then
    fail("GitHub returned no pull request URL")
  end
  return {url = created_url, state = "created"}
end

local function forgejo()
  local token = resolve_token()
  if token == "" then
    fail("Forgejo token is not configured")
  end
  local root = trim(settings.api_url)
  if root == "" then
    root = "https://" .. remote.host
  end
  root = api_root(root, "/api/v1")
  local headers = {Authorization = "token " .. token}
  local path = "/repos/" .. encode(remote.owner) .. "/" .. encode(remote.project) .. "/pulls"
  local list = request("GET", root .. path .. "?state=open&head=" .. encode(branch) .. "&base=" .. encode(base_branch), headers)
  if not success(list) then
    fail("Forgejo pull request lookup failed")
  end
  local existing_request, different_branch = find_response_request(list.body, integration_sha, branch, base_branch, true)
  if existing_request == nil then
    existing_request, different_branch = find_open_request_by_sha(root, path, headers, "Forgejo")
  end
  local existing = existing_request and existing_request.url or nil
  local existing_number = existing_request and existing_request.number or nil
  if existing ~= nil then
    if existing_number == nil then
      fail("Forgejo pull request lookup returned no request number")
    end
    if different_branch then
      return {url = existing, state = "existing"}
    end
    push_branch()
    local fields = {body = body}
    if requested_title ~= "" then
      fields.title = requested_title
    end
    local updated = request("PATCH", root .. path .. "/" .. existing_number, headers, fields)
    if not success(updated) then
      fail("Forgejo pull request update failed")
    end
    return {url = existing, state = "updated"}
  end
  push_branch()
  local created = request("POST", root .. path, headers, {title = title, head = branch, base = base_branch, body = body})
  if not success(created) then
    fail("Forgejo pull request creation failed")
  end
  local created_url = response_url(created.body)
  if created_url == nil then
    fail("Forgejo returned no pull request URL")
  end
  return {url = created_url, state = "created"}
end

local function gitlab()
  local token = resolve_token()
  if token == "" then
    fail("GitLab token is not configured")
  end
  local root = trim(settings.api_url)
  if root == "" then
    root = "https://" .. remote.host
  end
  root = api_root(root, "/api/v4")
  local headers = {["PRIVATE-TOKEN"] = token}
  local path = "/projects/" .. encode(remote.path) .. "/merge_requests"
  local list = request("GET", root .. path .. "?state=opened&source_branch=" .. encode(branch) .. "&target_branch=" .. encode(base_branch), headers)
  if not success(list) then
    fail("GitLab merge request lookup failed")
  end
  local existing, existing_number = response_request(list.body)
  if existing ~= nil then
    if existing_number == nil then
      fail("GitLab merge request lookup returned no request number")
    end
    push_branch()
    local fields = {description = body}
    if requested_title ~= "" then
      fields.title = requested_title
    end
    local updated = request_form("PUT", root .. path .. "/" .. existing_number, headers, fields)
    if not success(updated) then
      fail("GitLab merge request update failed")
    end
    return {url = existing, state = "updated"}
  end
  push_branch()
  local created = request_form("POST", root .. path, headers, {source_branch = branch, target_branch = base_branch, title = title, description = body})
  if not success(created) then
    fail("GitLab merge request creation failed")
  end
  local created_url = response_url(created.body)
  if created_url == nil then
    fail("GitLab returned no merge request URL")
  end
  return {url = created_url, state = "created"}
end

local provider_result
if forge == "github" then
  provider_result = github()
elseif forge == "forgejo" or forge == "gitea" then
  provider_result = forgejo()
else
  provider_result = gitlab()
end

return {repo = repo, url = provider_result.url, state = provider_result.state, branch = branch, base_branch = base_branch, title = title}
end

local results = {}
for _, entry in ipairs(selected_entries) do
  table.insert(results, create_one(entry, requested_title))
end

local result_lines = {}
local notification_lines = {}
local pull_requests = {}
local title = requested_title
if title == "" then
  title = results[1].title
end
for _, item in ipairs(results) do
  if item.state == "no_change" then
    table.insert(result_lines, item.repo .. ": no changes on " .. item.branch .. "; nothing to open")
    table.insert(notification_lines, item.repo .. ": no changes on " .. item.branch .. "; nothing to open")
  else
    table.insert(result_lines, item.repo .. ": " .. item.url .. " (" .. item.state .. ")")
    table.insert(notification_lines, item.repo .. ": " .. item.url .. " (" .. item.state .. ")")
    table.insert(pull_requests, {
      repo = item.repo,
      url = item.url,
      state = item.state,
      branch = item.branch,
      base_branch = item.base_branch,
      title = item.title
    })
  end
end
local result = table.concat(result_lines, "\n")
if #result > 4096 then
  fail("aggregate pull request result exceeds 4096 bytes")
end
local notification = "Pull request results:\n" .. table.concat(notification_lines, "\n")
local subject = "Pull request results: " .. title
data.result = result
data._omo_pull_requests = pull_requests
local notification_failed = false
for _, target in ipairs({"user", "ceo"}) do
  local _, notify_error = exec("omo", "send", "-t", target, "-s", subject, "-p", "normal", notification)
  if notify_error ~= nil then
    notification_failed = true
  end
end
omo.log("pull request results: " .. result)
if notification_failed then
  fail("pull request results prepared but notification failed")
end
