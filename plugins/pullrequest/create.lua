local data = event.data or {}
local settings = config or {}

local function trim(value)
  return string.gsub(tostring(value or ""), "^%s*(.-)%s*$", "%1")
end

local function fail(message)
  error("pullrequest: " .. message)
end

local function exec(...)
  local output, exec_error = omo.exec(...)
  if exec_error ~= "" then
    return nil, exec_error
  end
  return output, nil
end

local function require_text(name)
  local value = trim(data[name])
  if value == "" then
    fail("job metadata is missing " .. name)
  end
  return value
end

local job_id = trim(data.job_id)
if job_id == "" or job_id == "0" then
  fail("job metadata is required")
end

local args = data.args or {}
local selected_repo = ""
local title_index = 1
if args[1] ~= nil and string.match(trim(args[1]), "^repo=") then
  selected_repo = trim(string.sub(trim(args[1]), 6))
  if selected_repo == "" then
    fail("repo selector must name a repository")
  end
  title_index = 2
end
if #args >= title_index + 1 then
  if selected_repo == "" then
    fail("create accepts zero or one title argument")
  end
  fail("create accepts repo=<key> followed by at most one title")
end
local requested_title = ""
if args[title_index] ~= nil then
  requested_title = trim(args[title_index])
  if requested_title == "" then
    fail("title must not be empty")
  end
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
		fail("unknown repository " .. selected_repo .. "; no as-is integration branches are available (effective repository policy must be asis)")
	end
  fail("unknown repository " .. selected_repo .. "; valid keys: " .. table.concat(valid_repos, ", "))
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

local remote_url, remote_error = exec("git", "-C", worktree, "remote", "get-url", remote_name)
if remote_error ~= nil then
  fail("could not read the configured Git remote")
end
remote_url = trim(remote_url)

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

local function response_url(body)
  for _, key in ipairs({"html_url", "web_url", "url"}) do
    local value = string.match(body or "", '"' .. key .. '"%s*:%s*"(https?://[^"]+)"')
    if value ~= nil then
      return value
    end
  end
  return nil
end

local function cli_url(output)
  local value = string.match(output or "", "https?://[^%s]+")
  if value == nil then
    return nil
  end
  while string.match(value, "[\"'%,%]%}]$") do
    value = string.sub(value, 1, #value - 1)
  end
  return value
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

local function resolve_token()
  local configured = trim(settings.token)
  if configured ~= "" then
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
    return trim(output)
  end
  output, exec_error = exec("cmd.exe", "/C", "set", name)
  if exec_error == nil then
    local value = string.match(output or "", "^" .. name .. "=(.-)\r?\n?$")
    if value ~= nil and trim(value) ~= "" then
      return trim(value)
    end
  end
  return ""
end

local function load_job()
  local output, exec_error = exec("omo", "job", "show", job_id)
  if exec_error ~= nil then
    fail("could not read job details")
  end
  local title = string.match(output or "", "^title: ([^\n]*)")
  if title == nil then
    title = string.match(output or "", "\ntitle: ([^\n]*)")
  end
  local goal_start = string.find(output or "", "\ngoal:\n", 1, true)
  local goal = ""
  if goal_start ~= nil then
    goal = string.sub(output, goal_start + #"\ngoal:\n")
  end
  return trim(title), trim(goal)
end

local job_title, goal = load_job()
local title = requested_title
if title == "" then
  title = job_title
end
if title == "" then
  title = "Changes from " .. branch
end
if goal == "" then
  goal = "No goal summary was available."
end
local body = "OMO job " .. job_id .. " for " .. repo .. ".\n\n" .. goal

local _, push_error = exec("git", "-C", worktree, "push", "-u", remote_name, branch)
if push_error ~= nil then
  fail("Git push failed")
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
    local list_output, list_error = exec("gh", "pr", "list", "--repo", repo_path, "--head", branch, "--base", base_branch, "--state", "open", "--json", "url", "--limit", "1")
    if list_error ~= nil then
      fail("GitHub CLI lookup failed")
    end
    local existing = cli_url(list_output)
    if existing ~= nil then
      return existing
    end
    local created, create_error = exec("gh", "pr", "create", "--repo", repo_path, "--head", branch, "--base", base_branch, "--title", title, "--body", body)
    if create_error ~= nil then
      fail("GitHub CLI creation failed")
    end
    local created_url = cli_url(created)
    if created_url == nil then
      fail("GitHub CLI returned no pull request URL")
    end
    return created_url
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
  local existing = response_url(list.body)
  if existing ~= nil then
    return existing
  end
  local created = request("POST", root .. path, headers, {title = title, head = branch, base = base_branch, body = body})
  if not success(created) then
    fail("GitHub pull request creation failed")
  end
  local created_url = response_url(created.body)
  if created_url == nil then
    fail("GitHub returned no pull request URL")
  end
  return created_url
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
  local existing = response_url(list.body)
  if existing ~= nil then
    return existing
  end
  local created = request("POST", root .. path, headers, {title = title, head = branch, base = base_branch, body = body})
  if not success(created) then
    fail("Forgejo pull request creation failed")
  end
  local created_url = response_url(created.body)
  if created_url == nil then
    fail("Forgejo returned no pull request URL")
  end
  return created_url
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
  local existing = response_url(list.body)
  if existing ~= nil then
    return existing
  end
  local created = request_form("POST", root .. path, headers, {source_branch = branch, target_branch = base_branch, title = title, description = body})
  if not success(created) then
    fail("GitLab merge request creation failed")
  end
  local created_url = response_url(created.body)
  if created_url == nil then
    fail("GitLab returned no merge request URL")
  end
  return created_url
end

local url
if forge == "github" then
  url = github()
elseif forge == "forgejo" or forge == "gitea" then
  url = forgejo()
else
  url = gitlab()
end

return {repo = repo, url = url, branch = branch, base_branch = base_branch, title = title}
end

local created = {}
for _, entry in ipairs(selected_entries) do
  table.insert(created, create_one(entry, requested_title))
end

local result_lines = {}
local notification_lines = {}
local title = requested_title
if title == "" then
  title = created[1].title
end
for _, item in ipairs(created) do
  table.insert(result_lines, item.repo .. ": " .. item.url)
  table.insert(notification_lines, "- " .. item.repo .. ": " .. item.url .. " (" .. item.branch .. " -> " .. item.base_branch .. ")")
end
local result = table.concat(result_lines, "\n")
if #created == 1 then
  result = created[1].url
end
if #result > 4096 then
  fail("aggregate pull request result exceeds 4096 bytes")
end
local notification = "Pull requests created:\n" .. table.concat(notification_lines, "\n")
local subject = "Pull requests created: " .. title
data.result = result
local notification_failed = false
for _, target in ipairs({"user", "ceo"}) do
  local _, notify_error = exec("omo", "send", "-t", target, "-s", subject, "-p", "normal", notification)
  if notify_error ~= nil then
    notification_failed = true
  end
end
omo.log("pull requests created: " .. result)
if notification_failed then
  fail("pull requests created but notification failed")
end
