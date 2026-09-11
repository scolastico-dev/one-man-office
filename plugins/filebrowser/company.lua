local home = event.data.home_path
if type(home) ~= "string" or home == "" then
  error("filebrowser requires event.data.home_path")
end

local function windows()
  local output, err = omo.exec("uname", "-s")
  if err ~= "" or output == nil then return true end
  return output:match("MINGW") ~= nil or output:match("MSYS") ~= nil or output:match("Windows") ~= nil
end

local is_windows = windows()
local separator = is_windows and "\\" or "/"
local root = home .. separator .. "company" .. separator .. "http" .. separator .. "filebrowser"

local function join(base, name)
  if base:sub(-1) == "/" or base:sub(-1) == "\\" then return base .. name end
  return base .. separator .. name
end

local function powershell(script, ...)
  local output, err = omo.exec("pwsh", "-NoProfile", "-NonInteractive", "-Command", script, ...)
  if err ~= "" then
    output, err = omo.exec("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script, ...)
  end
  return output, err
end

local POSIX_SWEEP = "for entry in \"$1\"/* \"$1\"/.[!.]* \"$1\"/..?*; do [ -e \"$entry\" ] || [ -L \"$entry\" ] || continue; rm -rf -- \"$entry\"; done"
local POSIX_ENSURE_ROOT = [[set -eu
if [ -L "$1" ] || { [ -e "$1" ] && [ ! -d "$1" ]; }; then
  echo "filebrowser root must be an actual directory" >&2
  exit 1
fi
if [ ! -e "$1" ]; then mkdir -- "$1"; fi
if [ -L "$1" ] || [ ! -d "$1" ]; then
  echo "filebrowser root must be an actual directory" >&2
  exit 1
fi
chmod 700 -- "$1"]]
local WINDOWS_ENSURE_ROOT = [[param([string] $Root)
$item = Get-Item -LiteralPath $Root -Force -ErrorAction SilentlyContinue
if ($null -ne $item -and (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -or -not $item.PSIsContainer)) {
  throw "filebrowser root must be an actual directory"
}
if ($null -eq $item) { New-Item -ItemType Directory -Path $Root -ErrorAction Stop | Out-Null }
$item = Get-Item -LiteralPath $Root -Force -ErrorAction Stop
if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -or -not $item.PSIsContainer) {
  throw "filebrowser root must be an actual directory"
}]]
local WINDOWS_SWEEP = [[param([string] $Root)
if (Test-Path -LiteralPath $Root -PathType Container) {
  Get-ChildItem -LiteralPath $Root -Force | ForEach-Object {
    if ($_.Attributes -band [System.IO.FileAttributes]::ReparsePoint) { Remove-Item -LiteralPath $_.FullName -Force }
    else { Remove-Item -LiteralPath $_.FullName -Force -Recurse }
  }
}]]

local function ensure_root()
  local _, err
  if is_windows then
    _, err = powershell(WINDOWS_ENSURE_ROOT, root)
  else
    _, err = omo.exec("sh", "-c", POSIX_ENSURE_ROOT, "filebrowser-root", root)
  end
  if err ~= "" then error(err) end
end

local function sweep()
  ensure_root()
  local _, err
  if is_windows then
    _, err = powershell(WINDOWS_SWEEP, root)
  else
    _, err = omo.exec("sh", "-c", POSIX_SWEEP, "filebrowser-sweep", root)
  end
  if err ~= "" then error(err) end
end

local function random_id()
  if is_windows then
    local output, err = powershell([[($bytes = New-Object byte[] 16); $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create(); try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }; [BitConverter]::ToString($bytes).Replace('-', '')]])
    if err ~= "" then error(err) end
    return (output or ""):gsub("%s+", "")
  end
  local file, err = io.open("/dev/urandom", "rb")
  if not file then error(err or "cryptographic random source unavailable") end
  local bytes = file:read(16)
  file:close()
  if not bytes or #bytes ~= 16 then error("cryptographic random source returned too few bytes") end
  return (bytes:gsub(".", function(byte) return string.format("%02x", string.byte(byte)) end))
end

local function url_escape(value)
  return (value:gsub("[^A-Za-z0-9%-._~]", function(byte)
    return string.format("%%%02X", string.byte(byte))
  end))
end

if event.event == "company_startup" or event.event == "company_shutdown" then
  sweep()
  return
end

if event.event ~= "manual" or event.data.action ~= "download" then
  return
end

local args = event.data.args or {}
if #args ~= 1 then error("download requires exactly one absolute file path") end
local target = args[1]
if type(target) ~= "string" or target == "" or target:find("%z") or target:find("[\r\n]") then error("download path is invalid") end
if target:match("^\\\\") or target:match("^//[^/]") then error("UNC paths are not supported") end
if not target:match("^/") and not target:match("^%a:[\\/]") then error("download path must be absolute") end
local basename = target:match("[^/\\]+$")
if not basename or basename == "." or basename == ".." then error("download path has no file name") end

local _, stat_error
if is_windows then
  _, stat_error = powershell([[param([string] $Path)
$item = Get-Item -LiteralPath $Path -ErrorAction Stop
if ($item.PSIsContainer) { exit 1 }]], target)
else
  _, stat_error = omo.exec("test", "-f", target)
end
if stat_error ~= "" then error("download target is not a regular file") end

ensure_root()
local id, directory, link, make_error
for _ = 1, 8 do
  id = random_id()
  directory = join(root, id)
  if is_windows then
    _, make_error = powershell([[param([string] $Path)
New-Item -ItemType Directory -Path $Path -ErrorAction Stop | Out-Null]], directory)
  else
    _, make_error = omo.exec("mkdir", "-m", "700", directory)
  end
  if make_error == "" then break end
end
if make_error ~= "" then error("could not allocate a download directory") end

link = join(directory, basename)
if is_windows then
  local hard_link = [[param([string] $Target, [string] $Link)
New-Item -ItemType HardLink -Path $Link -Target $Target -ErrorAction Stop | Out-Null]]
  local symbolic_link = [[param([string] $Target, [string] $Link)
New-Item -ItemType SymbolicLink -Path $Link -Target $Target -ErrorAction Stop | Out-Null]]
  local copy = [[param([string] $Target, [string] $Link)
Copy-Item -LiteralPath $Target -Destination $Link -ErrorAction Stop]]
  local _, err = powershell(hard_link, target, link)
  if err ~= "" then _, err = powershell(symbolic_link, target, link) end
  if err ~= "" then _, err = powershell(copy, target, link) end
  if err ~= "" then error(err) end
else
  local _, err = omo.exec("ln", "-s", "--", target, link)
  if err ~= "" then error(err) end
end

return {url = "/filebrowser/" .. url_escape(id) .. "/" .. url_escape(basename)}
