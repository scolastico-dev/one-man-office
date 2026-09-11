'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
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
    if (args[3].includes('Test-Path')) return {code: 1};
    return {code: 0};
  };
  const commands = FilebrowserCommands.create(execute);
  await commands.select();
  assert.equal(await commands.exists('C:\\missing.txt'), false);
  assert.equal(await commands.isFile('C:\\missing.txt'), false);
  assert.deepEqual(calls.map(call => call.command), ['uname', 'pwsh', 'pwsh']);
});

test('Windows scripts are constant and hostile paths stay separate argv values', async () => {
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
    assert.deepEqual(call.args.slice(0, 3), ['-NoProfile', '-NonInteractive', '-Command']);
    const script = call.args[3];
    assert.equal(typeof script, 'string');
    for (const value of [...hostile, hostileFilter]) assert.equal(script.includes(value), false, `script must not contain ${value}`);
    assert.equal(script.includes('`'), false, 'script must not contain hostile backticks');
    assert.equal(script.includes('$(echo pwned)'), false, 'script must not contain hostile substitutions');
  }
  assert.equal(calls[0].args[3], calls[1].args[3], 'hostile paths must not alter the list script');
  assert.match(calls[0].args[3], /Get-ChildItem -Force \| Select Name,Length,LastWriteTimeUtc,Mode \| ConvertTo-Json -Compress/);
  assert.match(calls[2].args[3], /Get-ChildItem -Recurse -Filter/);
  assert.match(calls[6].args[3], /New-Item -ItemType Directory/);
  assert.match(calls[7].args[3], /\[Console\]::OpenStandardInput\(\)/);
  assert.match(calls[7].args[3], /FileStream/);
  assert.match(calls[7].args[3], /-LiteralPath \$Destination/);
  assert.notEqual(calls[0].args.indexOf(hostile[0]), -1);
  assert.notEqual(calls[1].args.indexOf(hostile[1]), -1);
  assert.notEqual(calls[2].args.indexOf(hostileFilter), -1);
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
