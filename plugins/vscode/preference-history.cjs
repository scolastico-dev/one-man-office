'use strict';
const fs = require('node:fs');
const path = require('node:path');
const {createHash, randomUUID} = require('node:crypto');
const {allowed} = require('./preference-sync.cjs');
const maxHistoryBytes = 32 * 1024 * 1024;

// The editor does not participate in the bridge's SQLite transaction. Record
// its write payloads before acknowledging saves, including writes that another
// bridge operation subsequently replaces. Only preference files are eligible.
function nativeHistory(directory) {
  return {
    record(name, value) {
      if (!allowed(name)) return;
      if (value.byteLength > maxHistoryBytes) throw new Error('preference history record is too large');
      const body = JSON.stringify({name, value: value.toString('base64')});
      if (Buffer.byteLength(body) > maxHistoryBytes) throw new Error('preference history record is too large');
      const id = createHash('sha256').update(body).digest('hex') + '.json';
      fs.mkdirSync(directory, {recursive: true, mode: 0o700});
      const temporary = path.join(directory, '.record-' + randomUUID());
      try {
        fs.writeFileSync(temporary, body, {mode: 0o600, flag: 'wx'});
        try { fs.linkSync(temporary, path.join(directory, id)); }
        catch (error) { if (error.code !== 'EEXIST') throw error; }
      } finally { try { fs.unlinkSync(temporary); } catch (error) { if (error.code !== 'ENOENT') throw error; } }
    },
    async entries() {
      let names;
      try { names = fs.readdirSync(directory); } catch (error) { if (error.code === 'ENOENT') return []; throw error; }
      return names.filter(name => /^[a-f0-9]{64}\.json$/.test(name)).map(id => {
        const file = path.join(directory, id), stat = fs.lstatSync(file);
        if (!stat.isFile() || stat.size > maxHistoryBytes) throw new Error('invalid preference history record');
        const entry = JSON.parse(fs.readFileSync(file, 'utf8'));
        if (!allowed(entry.name) || typeof entry.value !== 'string') throw new Error('invalid preference history record');
        return {id, name: entry.name, value: Buffer.from(entry.value, 'base64')};
      });
    },
    async acknowledge(entries) {
      for (const {id} of entries) {
        if (!/^[a-f0-9]{64}\.json$/.test(id)) throw new Error('invalid preference history identity');
        try { fs.unlinkSync(path.join(directory, id)); } catch (error) { if (error.code !== 'ENOENT') throw error; }
      }
    },
  };
}

function installNativeHistory(user, directory) {
  const history = nativeHistory(directory), handles = new Map(), restore = [];
  const originalOpen = fs.openSync.bind(fs), originalRead = fs.readSync.bind(fs), originalStat = fs.fstatSync.bind(fs), originalClose = fs.closeSync.bind(fs);
  function nameOf(file) {
    if (typeof file !== 'string' && !Buffer.isBuffer(file) && !(file instanceof URL)) return;
    const relative = path.relative(user, file instanceof URL ? require('node:url').fileURLToPath(file) : String(file)).split(path.sep).join('/');
    // DiskFileSystemProvider uses this suffix for its atomic preference saves.
    const name = relative.endsWith('.vsctmp') ? relative.slice(0, -'.vsctmp'.length) : relative;
    if (allowed(name)) return name;
  }
  function previous(file, name) {
    if (!name) return;
    let fd;
    try {
      fd = originalOpen(file, fs.constants.O_RDONLY | (fs.constants.O_NONBLOCK || 0));
      const stat = originalStat(fd);
      if (stat.isFile() && stat.size <= maxHistoryBytes) {
        const value = Buffer.alloc(stat.size + 1); let count = 0;
        while (count < value.length) {
          const read = originalRead(fd, value, count, value.length - count, count);
          if (!read) { const result = value.subarray(0, count); history.record(name, result); return result; }
          count += read;
        }
      }
    } catch (error) { if (error.code !== 'ENOENT') throw error; }
    finally { if (fd !== undefined) originalClose(fd); }
  }
  function payload(data, options) {
    if (typeof data === 'string') return Buffer.from(data, typeof options === 'string' ? options : options?.encoding || 'utf8');
    if (ArrayBuffer.isView(data)) return Buffer.from(data.buffer, data.byteOffset, data.byteLength);
  }
  function capture(file, data, options) {
    const name = nameOf(file);
    if (!name) return;
    previous(file, name);
    const value = payload(data, options);
    if (value) history.record(name, value);
  }
  function replace(object, key, make) {
    const original = object[key], replacement = make(original);
    // fs.write's promisify metadata supplies {bytesWritten, buffer}. Dropping
    // its symbols changes the publisher's I/O contract despite valid bytes.
    Object.defineProperties(replacement, Object.getOwnPropertyDescriptors(original));
    object[key] = replacement;
    restore.push(() => { object[key] = original; });
  }
  for (const key of ['writeFile', 'writeFileSync']) replace(fs, key, original => function(file, data, ...args) {
    capture(file, data, args[0]); return original.call(this, file, data, ...args);
  });
  replace(fs.promises, 'writeFile', original => async function(file, data, options) {
    capture(file, data, options); return original.call(this, file, data, options);
  });
  function opened(file, flags) {
    const writable = typeof flags === 'number' ? flags & (fs.constants.O_WRONLY | fs.constants.O_RDWR) : /[wa+]/.test(flags);
    const name = writable && nameOf(file);
    if (!name) return;
    const old = previous(file, name);
    const truncate = typeof flags === 'number' ? flags & fs.constants.O_TRUNC : flags.startsWith('w');
    return {name, value: truncate ? Buffer.alloc(0) : old || Buffer.alloc(0), position: 0};
  }
  replace(fs, 'open', original => function(file, flags, ...args) {
    const handle = opened(file, flags), callback = args.pop();
    return original.call(this, file, flags, ...args, (error, fd) => {
      if (!error && handle) handles.set(fd, handle);
      callback(error, fd);
    });
  });
  replace(fs, 'openSync', original => function(file, flags, ...args) {
    const handle = opened(file, flags), fd = original.call(this, file, flags, ...args);
    if (handle) handles.set(fd, handle);
    return fd;
  });
  function written(fd, buffer, offset, length, position) {
    const handle = handles.get(fd);
    if (!handle) return;
    // The pinned disk provider writes a Uint8Array with explicit offsets. Other
    // writers remain covered by their complete writeFile payload above.
    if (!ArrayBuffer.isView(buffer) || typeof offset !== 'number' || typeof length !== 'number') return;
    const start = position ?? handle.position, end = start + length;
    if (end > maxHistoryBytes) throw new Error('preference history record is too large');
    const value = Buffer.alloc(Math.max(handle.value.length, end));
    handle.value.copy(value);
    Buffer.from(buffer.buffer, buffer.byteOffset + offset, length).copy(value, start);
    handle.value = value; handle.position = end;
  }
  for (const key of ['write', 'writeSync']) replace(fs, key, original => function(fd, buffer, ...args) {
    written(fd, buffer, args[0], args[1], args[2]);
    return original.call(this, fd, buffer, ...args);
  });
  for (const key of ['close', 'closeSync']) replace(fs, key, original => function(fd, ...args) {
    const handle = handles.get(fd);
    if (handle) history.record(handle.name, handle.value);
    handles.delete(fd); return original.call(this, fd, ...args);
  });
  return () => { for (const undo of restore.reverse()) undo(); };
}
module.exports = {nativeHistory, installNativeHistory, maxHistoryBytes};
