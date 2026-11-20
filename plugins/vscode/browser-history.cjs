(function () {
  'use strict';
  // Prepended to the pinned OpenVSCode workbench. Journal in the SAME IndexedDB
  // readwrite transaction as the preference mutation; extension API preflight
  // reads, file watchers and a separate rename cannot close this interval.
  function installBrowserHistory(scope) {
    const prototype = scope.IDBObjectStore.prototype;
    const original = {put: prototype.put, delete: prototype.delete, get: prototype.get};
    const transactions = new WeakMap();
    const journal = '/User/.omo-preference-history.json', acknowledgement = '/User/.omo-preference-ack.json';
    const decoder = new TextDecoder('utf-8', {ignoreBOM: true}), encoder = new TextEncoder();
    function bytes(value) { return typeof value === 'string' ? encoder.encode(value) : value; }
    function nameOf(key) {
      if (typeof key !== 'string' || !key.startsWith('/User/')) return;
      const name = key.slice(6);
      if (['settings.json', 'keybindings.json', 'tasks.json'].includes(name) ||
        (name.startsWith('snippets/') && name.split('/').every(part => part && part !== '.' && part !== '..' && !part.includes('\\')))) return name;
    }
    function encoded(value) {
      let text = '';
      for (let start = 0; start < value.length; start += 8192) text += String.fromCharCode(...value.subarray(start, start + 8192));
      return scope.btoa(text);
    }
    function state(store) {
      let entry = transactions.get(store.transaction);
      if (!entry) {
        entry = {pending: []}; transactions.set(store.transaction, entry);
        const request = original.get.call(store, journal);
        request.onsuccess = () => {
          try {
            entry.value = request.result === undefined ? {version: 1, next: 1, entries: []} : JSON.parse(decoder.decode(bytes(request.result)));
            if (entry.value.version !== 1 || !Array.isArray(entry.value.entries)) throw new Error('unsupported preference history');
            for (const change of entry.pending) change(entry.value);
            entry.pending = [];
            persist(store, entry.value);
          } catch { store.transaction.abort(); }
        };
      }
      return entry;
    }
    function persist(store, value) {
      const content = encoder.encode(JSON.stringify(value));
      if (content.byteLength > 32 * 1024 * 1024 || value.entries.length > 4096) throw new Error('unacknowledged preference history is full');
      original.put.call(store, content, journal);
    }
    function change(store, update) {
      const entry = state(store);
      if (!entry.value) { entry.pending.push(update); return; }
      try { update(entry.value); persist(store, entry.value); } catch { store.transaction.abort(); }
    }
    function record(store, name, value) {
      value = bytes(value);
      if (!(value instanceof Uint8Array)) throw new Error('unsupported preference payload');
      if (value.byteLength > 32 * 1024 * 1024) throw new Error('preference history record is too large');
      const content = encoded(value);
      change(store, history => {
        if (!Number.isSafeInteger(history.next)) throw new Error('preference history identity overflow');
        history.entries.push({id: history.next++, name, value: content});
      });
    }
    function eligible(store) { return store.name === 'vscode-userdata-store' && store.transaction.db.name === 'vscode-web-db'; }
    prototype.put = function (value, key) {
      if (eligible(this)) {
        const name = nameOf(key);
        if (name) record(this, name, value);
        else if (key === acknowledgement) {
          const ack = JSON.parse(decoder.decode(bytes(value)));
          if (!Number.isSafeInteger(ack.through) || ack.through < 0) throw new Error('invalid preference history acknowledgement');
          change(this, history => {
            history.entries = history.entries.filter(entry => entry.id > ack.through);
            if (typeof ack.nonce === 'string') history.ready = ack.nonce;
          });
        }
      }
      return original.put.call(this, value, key);
    };
    prototype.delete = function (key) {
      const name = eligible(this) && nameOf(key);
      if (name) {
        // Snapshot the value being removed within this very transaction. A save
        // committed before this delete is therefore never silently discarded.
        const request = original.get.call(this, key);
        state(this);
        request.onsuccess = () => { if (request.result !== undefined) record(this, name, request.result); };
      }
      return original.delete.call(this, key);
    };
    return () => { prototype.put = original.put; prototype.delete = original.delete; };
  }
  if (typeof module !== 'undefined' && module.exports) module.exports = {installBrowserHistory};
  else if (typeof IDBObjectStore !== 'undefined') installBrowserHistory(globalThis);
})();
