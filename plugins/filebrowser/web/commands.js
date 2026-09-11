'use strict';

(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.FilebrowserCommands = factory();
})(typeof globalThis === 'object' ? globalThis : this, function () {
  const WINDOWS_LIST = `param([string] $Path, [bool] $IncludeHidden, [bool] $DirectoriesOnly)
Set-Location -LiteralPath $Path
if ($IncludeHidden -and -not $DirectoriesOnly) {
  Get-ChildItem -Force | Select Name,Length,LastWriteTimeUtc,Mode | ConvertTo-Json -Compress
  exit 0
}
$items = Get-ChildItem -Force
if (-not $IncludeHidden) { $items = $items | Where-Object { -not ($_.Attributes -band [System.IO.FileAttributes]::Hidden) -and -not $_.Name.StartsWith('.') } }
if ($DirectoriesOnly) { $items = $items | Where-Object { $_.PSIsContainer } }
$items | Select Name,Length,LastWriteTimeUtc,Mode | ConvertTo-Json -Compress`;
  const WINDOWS_SEARCH = `param([string] $Path, [string] $Filter)
Set-Location -LiteralPath $Path
Get-ChildItem -Recurse -Filter $Filter | Select Name,Length,LastWriteTimeUtc,Mode | ConvertTo-Json -Compress`;
  const WINDOWS_HOME = 'Get-Location | Select-Object -ExpandProperty Path';
  const WINDOWS_SIZE = `param([string] $Path)
(Get-Item -LiteralPath $Path).Length`;
  const WINDOWS_EXISTS = `param([string] $Path)
if (Test-Path -LiteralPath $Path) { exit 0 } else { exit 1 }`;
  const WINDOWS_IS_FILE = `param([string] $Path)
if (Test-Path -LiteralPath $Path -PathType Leaf) { exit 0 } else { exit 1 }`;
  const WINDOWS_MKDIR = `param([string] $Path)
$EscapedPath = [System.Management.Automation.WildcardPattern]::Escape($Path)
New-Item -ItemType Directory -Path $EscapedPath`;
  const WINDOWS_UPLOAD = `param([string] $Destination)
$inputStream = [Console]::OpenStandardInput()
$outputStream = [System.IO.FileStream]::new($Destination, [System.IO.FileMode]::Create, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
try { $inputStream.CopyTo($outputStream) } finally { $outputStream.Dispose(); $inputStream.Dispose() }`;
  function isUNCPath(path) {
    return typeof path === 'string' && (/^\\\\/.test(path) || /^\/\/[^/\\]/.test(path));
  }

  function assertPath(path, {allowLineBreaks = false} = {}) {
    if (isUNCPath(path)) throw new Error('UNC paths are not supported.');
    if (typeof path !== 'string' || !(/^(?:[A-Za-z]:[\\/]|\/)/.test(path))) throw new Error('Enter an absolute path.');
    if (/\0/.test(path) || (!allowLineBreaks && /[\r\n]/.test(path))) throw new Error('Paths cannot contain NUL or line breaks.');
    return path;
  }

  function collectOutput(options) {
    const events = [];
    return {
      events,
      onOutput(event) {
        events.push(event);
        options.onOutput?.(event);
      },
    };
  }

  function stdout(events) {
    return events.filter(event => event?.stream === 'stdout').map(event => String(event.data || '')).join('');
  }

  function escapeXml(value) {
    return String(value).replace(/[&<>"']/g, character => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;'}[character]));
  }

  function encodeUtf16LEBase64(value) {
    const bytes = new Uint8Array(value.length * 2);
    for (let index = 0; index < value.length; index++) {
      const codeUnit = value.charCodeAt(index);
      bytes[index * 2] = codeUnit & 0xff;
      bytes[index * 2 + 1] = codeUnit >> 8;
    }
    if (typeof Buffer === 'function') return Buffer.from(bytes).toString('base64');
    let binary = '';
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return btoa(binary);
  }

  function encodePowerShellArguments(values) {
    const entries = values.map(value => typeof value === 'boolean' ? `<B>${value ? 'true' : 'false'}</B>` : `<S>${escapeXml(value)}</S>`).join('');
    const xml = `<Objs Version="1.1.0.1" xmlns="http://schemas.microsoft.com/powershell/2004/04"><Obj RefId="0"><TN RefId="0"><T>System.Collections.ArrayList</T><T>System.Object</T></TN><LST>${entries}</LST></Obj></Objs>`;
    return encodeUtf16LEBase64(xml);
  }

  function parseListing(output) {
    let value;
    try { value = JSON.parse(String(output || '[]')); }
    catch { throw new Error('The file listing was not valid JSON.'); }
    const records = Array.isArray(value) ? value : value && typeof value === 'object' ? [value] : [];
    let skippedNewlineNames = false;
    const entries = [];
    for (const record of records) {
      const name = String(record?.Name ?? '');
      if (!name || /[\r\n]/.test(name)) { skippedNewlineNames = true; continue; }
      const directory = String(record?.Mode || '').toLowerCase().startsWith('d') || record?.PSIsContainer === true;
      const rawSize = record?.Length;
      entries.push({name, type: directory ? 'directory' : 'file', size: directory || rawSize == null ? null : Number(rawSize)});
    }
    return {entries, skippedNewlineNames};
  }

  function create(execute) {
    if (typeof execute !== 'function') throw new TypeError('Filebrowser commands require an execute function.');
    let adapter = null;
    let shell = 'pwsh';
    let probedHome = null;

    async function run(command, args, options = {}) {
      const output = collectOutput(options);
      const result = await execute(command, args, {...options, onOutput: output.onOutput});
      if (result && typeof result.code === 'number' && result.code !== 0) throw new Error(`Command ${command} exited with status ${result.code}.`);
      return {result, events: output.events, stdout: stdout(output.events)};
    }

    async function runWindows(script, values, options = {}) {
      const args = ['-NoProfile', '-NonInteractive'];
      if (values.length) args.push('-EncodedArguments', encodePowerShellArguments(values));
      args.push('-Command', script);
      try {
        return await run(shell, args, options);
      } catch (error) {
        if (shell !== 'pwsh' || !isExecutableUnavailable(error)) throw error;
        shell = 'powershell.exe';
        return run(shell, args, options);
      }
    }

    function isExecutableUnavailable(error) {
      if (error?.result) return false;
      return /(?:executable file not found|command not found|not recognized|cannot find|no such file)/i.test(String(error?.message || error));
    }

    function requireAdapter() {
      if (!adapter) throw new Error('The file manager platform has not been selected.');
      return adapter;
    }

    async function select() {
      let result;
      try {
        result = await run('uname', ['-s']);
        const platform = result.stdout.trim().split(/\r?\n/)[0];
        if (/^(?:linux|darwin|freebsd|openbsd|netbsd|dragonfly|sunos|aix)/i.test(platform)) {
          adapter = 'posix';
          return adapter;
        }
      } catch {
        // Native Windows does not provide uname; probe PowerShell below.
      }
      try {
        probedHome = await runWindows(WINDOWS_HOME, []);
      } catch {
        throw new Error('File manager unavailable: unable to select an adapter; ensure uname, pwsh, or powershell.exe is available and try again.');
      }
      adapter = 'windows';
      return adapter;
    }

    async function posixHome() {
      return (await run('pwd', [])).stdout.trim().split(/\r?\n/)[0] || '/';
    }

    async function posixList(path, options = {}) {
      const directoryEvents = [];
      const find = async type => {
        const args = [path, '-mindepth', '1', '-maxdepth', '1'];
        if (type === 'directory') args.push('-type', 'd');
        else args.push('!', '-type', 'd');
        args.push('-print0');
        const result = await run('find', args, {signal: options.signal, onOutput: event => { directoryEvents.push(event); options.onOutput?.(event); }});
        const entries = [];
        let skippedNewlineNames = false;
        for (const raw of result.stdout.split('\0')) {
          if (!raw) continue;
          const name = raw.slice(raw.lastIndexOf('/') + 1);
          if (!name || /[\r\n]/.test(name)) { skippedNewlineNames = true; continue; }
          entries.push({name, type, size: type === 'directory' ? null : undefined});
        }
        return {entries, skippedNewlineNames};
      };
      const directories = await find('directory');
      const files = options.directoriesOnly ? {entries: [], skippedNewlineNames: false} : await find('file');
      const hidden = options.includeHidden;
      const entries = [...files.entries, ...directories.entries].filter(entry => hidden || !entry.name.startsWith('.'));
      for (const entry of entries) {
        if (entry.type === 'directory') continue;
        const entryPath = path === '/' ? `/${entry.name}` : `${path}/${entry.name}`;
        try { entry.size = Number((await run('wc', ['-c', entryPath], {signal: options.signal, onOutput: options.onOutput})).stdout.trim().split(/\s+/)[0]); }
        catch { entry.size = null; }
      }
      return {entries, skippedNewlineNames: directories.skippedNewlineNames || files.skippedNewlineNames};
    }

    async function posixSearch(path, filter, options = {}) {
      assertPath(path);
      const result = await run('find', [path, '-type', 'f', '-name', filter, '-print0'], options);
      let skippedNewlineNames = false;
      const entries = [];
      for (const value of result.stdout.split('\0')) {
        if (!value) continue;
        const name = value.slice(value.lastIndexOf('/') + 1);
        if (!name || /[\r\n]/.test(name)) { skippedNewlineNames = true; continue; }
        entries.push({name, type: 'file', size: null});
      }
      return {entries, skippedNewlineNames};
    }

    async function posixSize(path, options = {}) {
      assertPath(path);
      const value = (await run('wc', ['-c', path], options)).stdout.trim().split(/\s+/)[0];
      const size = Number(value);
      return Number.isFinite(size) ? size : null;
    }

    async function posixExists(path, options = {}) { assertPath(path); try { await run('test', ['-e', path], options); return true; } catch { return false; } }
    async function posixIsFile(path, options = {}) { assertPath(path); try { await run('test', ['-f', path], options); return true; } catch { return false; } }
    async function posixMkdir(path, options = {}) { assertPath(path); return run('mkdir', [path], options); }
    async function posixUpload(path, options = {}) { assertPath(path); return run('dd', [`of=${path}`], options); }
    async function windowsHome() {
      const result = probedHome || await runWindows(WINDOWS_HOME, []);
      probedHome = null;
      return result.stdout.trim().split(/\r?\n/)[0] || '';
    }
    async function windowsList(path, options = {}) {
      assertPath(path);
      const result = await runWindows(WINDOWS_LIST, [path, Boolean(options.includeHidden), Boolean(options.directoriesOnly)], options);
      return parseListing(result.stdout);
    }
    async function windowsSearch(path, filter, options = {}) {
      assertPath(path);
      const result = await runWindows(WINDOWS_SEARCH, [path, String(filter)], options);
      return parseListing(result.stdout);
    }
    async function windowsSize(path, options = {}) {
      assertPath(path);
      const value = (await runWindows(WINDOWS_SIZE, [path], options)).stdout.trim();
      const size = Number(value);
      return Number.isFinite(size) ? size : null;
    }
    async function windowsExists(path, options = {}) { assertPath(path); try { await runWindows(WINDOWS_EXISTS, [path], options); return true; } catch { return false; } }
    async function windowsIsFile(path, options = {}) { assertPath(path); try { await runWindows(WINDOWS_IS_FILE, [path], options); return true; } catch { return false; } }
    async function windowsMkdir(path, options = {}) { assertPath(path, {allowLineBreaks: true}); return runWindows(WINDOWS_MKDIR, [path], options); }
    async function windowsUpload(path, options = {}) { assertPath(path); return runWindows(WINDOWS_UPLOAD, [path], options); }
    async function home() { return requireAdapter() === 'posix' ? posixHome() : windowsHome(); }
    async function list(path, options = {}) { return requireAdapter() === 'posix' ? posixList(assertPath(path), options) : windowsList(path, options); }
    async function search(path, filter, options = {}) { return requireAdapter() === 'posix' ? posixSearch(path, filter, options) : windowsSearch(path, filter, options); }
    async function size(path, options = {}) { return requireAdapter() === 'posix' ? posixSize(path, options) : windowsSize(path, options); }
    async function stat(path, options = {}) { return {size: await size(path, options)}; }
    async function exists(path, options = {}) { return requireAdapter() === 'posix' ? posixExists(path, options) : windowsExists(path, options); }
    async function isFile(path, options = {}) { return requireAdapter() === 'posix' ? posixIsFile(path, options) : windowsIsFile(path, options); }
    async function mkdir(path, options = {}) { return requireAdapter() === 'posix' ? posixMkdir(path, options) : windowsMkdir(path, options); }
    async function upload(path, options = {}) { return requireAdapter() === 'posix' ? posixUpload(path, options) : windowsUpload(path, options); }
    /**
     * The UI-facing command boundary. PowerShell scripts above are immutable;
     * paths and names are serialized as data for its parameter binder, so
     * command text never contains user input.
     */
    return Object.freeze({select, home, list, search, size, stat, exists, isFile, mkdir, upload, platform: () => adapter, shell: () => shell});
  }

  return Object.freeze({create});
});
