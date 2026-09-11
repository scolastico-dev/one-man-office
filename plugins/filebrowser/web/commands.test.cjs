'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {spawn, spawnSync} = require('node:child_process');
const FilebrowserCommands = require('./commands.js');

function windowsHarness({fallback = false} = {}) {
  const calls = [];
  const execute = async (command, args, options = {}) => {
    calls.push({command, args, options});
    if (command === 'uname') {
      options.onOutput?.({stream: 'stdout', data: 'MINGW64_NT-10.0-22631\n'});
      return {code: 0};
    }
    if (fallback && command === 'pwsh') throw new Error('exec: "pwsh": executable file not found in $PATH');
    if (command === 'pwsh' || command === 'powershell.exe') {
      options.onOutput?.({stream: 'stdout', data: 'C:\\Users\\demo\\\n'});
      return {code: 0};
    }
    throw new Error(`unexpected command ${command}`);
  };
  return {calls, execute};
}

test('selects pwsh and falls back to powershell.exe for Windows hosts', async () => {
  const harness = windowsHarness({fallback: true});
  const commands = FilebrowserCommands.create(harness.execute);
  await commands.select();
  assert.equal(await commands.home(), 'C:\\Users\\demo\\');
  assert.deepEqual(harness.calls.map(call => call.command), ['uname', 'pwsh', 'powershell.exe']);
  assert.deepEqual(harness.calls[1].args.slice(0, 3), ['-NoProfile', '-NonInteractive', '-Command']);
  assert.deepEqual(harness.calls[2].args.slice(0, 3), ['-NoProfile', '-NonInteractive', '-Command']);
});

test('treats unavailable uname as Windows and probes PowerShell', async () => {
  const calls = [];
  const execute = async (command, args, options = {}) => {
    calls.push({command, args});
    if (command === 'uname') throw new Error('command not found');
    options.onOutput?.({stream: 'stdout', data: 'C:\\Users\\native\\\n'});
    return {code: 0};
  };
  const commands = FilebrowserCommands.create(execute);
  assert.equal(await commands.select(), 'windows');
  assert.equal(await commands.home(), 'C:\\Users\\native\\');
  assert.deepEqual(calls.map(call => call.command), ['uname', 'pwsh']);
});

test('does not switch shells when PowerShell reports false for exists or is-file', async () => {
  const calls = [];
  const execute = async (command, args, options = {}) => {
    calls.push({command, args});
    if (command === 'uname') {
      options.onOutput?.({stream: 'stdout', data: 'Windows_NT\n'});
      return {code: 0};
    }
    if (args[args.indexOf('-Command') + 1].includes('Test-Path')) return {code: 1};
    return {code: 0};
  };
  const commands = FilebrowserCommands.create(execute);
  await commands.select();
  assert.equal(await commands.exists('C:\\missing.txt'), false);
  assert.equal(await commands.isFile('C:\\missing.txt'), false);
  assert.deepEqual(calls.map(call => call.command), ['uname', 'pwsh', 'pwsh']);
});

test('Windows scripts are constant and hostile paths stay encoded arguments', async () => {
  const harness = windowsHarness();
  harness.execute = async (command, args, options = {}) => {
    harness.calls.push({command, args, options});
    if (command === 'uname') {
      options.onOutput?.({stream: 'stdout', data: 'Windows_NT\n'});
      return {code: 0};
    }
    options.onOutput?.({stream: 'stdout', data: '[]'});
    return {code: 0};
  };
  const commands = FilebrowserCommands.create(harness.execute);
  await commands.select();
  const hostile = [
    'C:\\Temp\\$(echo pwned) `tick` "quote"; semi',
    'C:\\Temp\\line "quoted"; semi',
  ];
  const hostileFilter = 'name\r\n$(pattern);`tick`';
  await commands.list(hostile[0]);
  await commands.list(hostile[1]);
  const searchResult = await commands.search(hostile[0], hostileFilter);
  assert.deepEqual(searchResult.entries, []);
  await commands.size(hostile[0]);
  await commands.exists(hostile[0]);
  await commands.isFile(hostile[0]);
  await commands.mkdir(hostile[0]);
  await commands.upload(hostile[0], {stdin: {name: 'upload'}});

  const calls = harness.calls.slice(1);
  assert.ok(calls.length >= 8);
  for (const call of calls) {
    assert.equal(call.command, 'pwsh');
    assert.deepEqual(call.args.slice(0, 2), ['-NoProfile', '-NonInteractive']);
    const encodedIndex = call.args.indexOf('-EncodedArguments');
    const commandIndex = call.args.indexOf('-Command');
    assert.ok(encodedIndex > 1 && commandIndex > encodedIndex);
    const script = call.args[commandIndex + 1];
    assert.equal(typeof script, 'string');
    for (const value of [...hostile, hostileFilter]) assert.equal(script.includes(value), false, `script must not contain ${value}`);
    assert.equal(script.includes('`'), false, 'script must not contain hostile backticks');
    assert.equal(script.includes('$(echo pwned)'), false, 'script must not contain hostile substitutions');
    for (const value of [...hostile, hostileFilter]) assert.equal(call.args.includes(value), false, `raw argv must not contain ${value}`);
  }
  const scriptAt = call => call.args[call.args.indexOf('-Command') + 1];
  assert.equal(scriptAt(calls[0]), scriptAt(calls[1]), 'hostile paths must not alter the list script');
  assert.match(scriptAt(calls[0]), /Get-ChildItem -Force \| Select Name,Length,LastWriteTimeUtc,Mode \| ConvertTo-Json -Compress/);
  assert.match(scriptAt(calls[2]), /Get-ChildItem -Recurse -Filter/);
  assert.match(scriptAt(calls[6]), /\[System\.IO\.Directory\]::CreateDirectory\(\$Path\)/);
  assert.match(scriptAt(calls[7]), /\[Console\]::OpenStandardInput\(\)/);
  assert.match(scriptAt(calls[7]), /FileStream/);
  assert.doesNotMatch(scriptAt(calls[7]), /New-Item/);
  const encoded = calls[0].args[calls[0].args.indexOf('-EncodedArguments') + 1];
  const decoded = Buffer.from(encoded, 'base64').toString('utf16le');
  assert.match(decoded, /C:\\Temp\\\$\(echo pwned\) `tick` &quot;quote&quot;; semi/);
  assert.match(decoded, /<B>false<\/B><B>false<\/B>/);
});

test('Windows listing maps JSON records to current entry semantics and rejects UNC paths', async () => {
  const harness = windowsHarness();
  harness.execute = async (command, args, options = {}) => {
    harness.calls.push({command, args, options});
    if (command === 'uname') {
      options.onOutput?.({stream: 'stdout', data: 'Windows_NT\n'});
      return {code: 0};
    }
    options.onOutput?.({stream: 'stdout', data: '[{"Name":"docs","Length":null,"LastWriteTimeUtc":"2026-01-01T00:00:00Z","Mode":"d----"},{"Name":"report.txt","Length":12,"LastWriteTimeUtc":"2026-01-01T00:00:00Z","Mode":"-a---"}]'});
    return {code: 0};
  };
  const commands = FilebrowserCommands.create(harness.execute);
  await commands.select();
  const result = await commands.list('C:\\Temp\\', {includeHidden: true});
  assert.deepEqual(result.entries, [
    {name: 'docs', type: 'directory', size: null},
    {name: 'report.txt', type: 'file', size: 12},
  ]);
  await assert.rejects(() => commands.list('\\\\server\\share'), /UNC paths are not supported/);
  assert.equal(harness.calls.length, 2, 'UNC validation must happen before execute');
});

function findRealPowerShell() {
  const configured = process.env.FILEBROWSER_PWSH;
  const candidates = configured ? [{command: 'pwsh', executable: configured}] : [
    {command: 'pwsh', executable: 'pwsh'},
    {command: 'powershell.exe', executable: 'powershell.exe'},
  ];
  return candidates.find(candidate => spawnSync(candidate.executable, ['-NoProfile', '-NonInteractive', '-Command', 'exit 0'], {stdio: 'ignore'}).status === 0) || null;
}

const realPowerShell = findRealPowerShell();

test('Windows adapter executes parameterized commands with real PowerShell', {skip: !realPowerShell}, async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'filebrowser-powershell-'));
  const calls = [];
  const execute = (command, args, options = {}) => new Promise((resolve, reject) => {
    calls.push({command, args});
    if (command === 'uname') {
      options.onOutput?.({stream: 'stdout', data: 'Windows_NT\n'});
      resolve({code: 0});
      return;
    }
    if (command !== realPowerShell.command) {
      reject(new Error(`exec: "${command}": executable file not found in $PATH`));
      return;
    }
    const child = spawn(realPowerShell.executable, args, {stdio: ['pipe', 'pipe', 'pipe']});
    let settled = false;
    const output = (stream, data) => options.onOutput?.({stream, data: data.toString()});
    child.stdout.on('data', data => output('stdout', data));
    child.stderr.on('data', data => output('stderr', data));
    child.on('error', reject);
    child.on('close', code => { settled = true; resolve({code}); });
    if (options.stdin == null) child.stdin.end();
    else if (typeof options.stdin === 'string' || Buffer.isBuffer(options.stdin)) child.stdin.end(options.stdin);
    else child.stdin.end();
    options.signal?.addEventListener('abort', () => { if (!settled) child.kill(); }, {once: true});
  });

  try {
    const commands = FilebrowserCommands.create(execute);
    assert.equal(await commands.select(), 'windows');
    fs.writeFileSync(path.join(root, 'visible.txt'), 'visible');
    fs.writeFileSync(path.join(root, '.hidden.txt'), 'hidden');
    const directory = path.join(root, 'folder[1] with spaces');
    await commands.mkdir(directory);
    assert.equal(fs.statSync(directory).isDirectory(), true);
    const normal = await commands.list(root);
    assert.deepEqual(normal.entries.map(entry => entry.name).sort(), ['folder[1] with spaces', 'visible.txt']);
    const withHidden = await commands.list(root, {includeHidden: true});
    assert.deepEqual(withHidden.entries.map(entry => entry.name).sort(), ['.hidden.txt', 'folder[1] with spaces', 'visible.txt']);
    const directories = await commands.list(root, {directoriesOnly: true});
    assert.deepEqual(directories.entries.map(entry => entry.name), ['folder[1] with spaces']);
    const upload = path.join(root, 'upload[1] "quoted"; semi.bin');
    const binary = Buffer.from([0, 255, 10, 13, 42]);
    await commands.upload(upload, {stdin: binary});
    assert.deepEqual(fs.readFileSync(upload), binary);
    assert.equal(await commands.exists(upload), true);
    assert.equal(await commands.isFile(upload), true);
    assert.equal(await commands.size(upload), binary.length);
    const marker = path.join(root, 'injected-marker');
    const hostilePath = `C:\\Temp\\$(New-Item -ItemType File -Path '${marker}') \`tick\` "quote"; semi space`;
    const hostileFilter = `*.txt\r\n$(New-Item -ItemType File -Path '${marker}') ; \`tick\` "quote"`;
    assert.equal(await commands.exists(hostilePath), false);
    try { await commands.search(root, hostileFilter); } catch { /* The provider may reject a filter containing a line break. */ }
    assert.equal(fs.existsSync(marker), false);
    assert.ok(calls.some(call => call.command === realPowerShell.command && call.args.includes('-EncodedArguments')));
  } finally {
    fs.rmSync(root, {recursive: true, force: true});
  }
});
