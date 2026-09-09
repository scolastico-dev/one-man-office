'use strict';
(() => {
  const token = location.hash.slice(1);
  history.replaceState(null, '', location.pathname);
  const $ = id => document.getElementById(id);
  const terminals = new Map();
  const stateListeners = new Set();
  let selected = null;
  let state = {projects: [], instances: []};
  const notice = text => { $('notice').textContent = text; };
  const showAPIError = error => notice(!token && error.status === 401 ? 'Open the access URL printed by omo supervisor. The access key stays in this page’s memory; reload using that original URL.' : error.message);
  async function api(path, method = 'GET', body) {
    const headers = {'Content-Type': 'application/json'};
    if (token) headers.Authorization = 'Bearer ' + token;
    const response = await fetch('/api/' + path, {method, headers, body: body === undefined ? undefined : JSON.stringify(body), cache: 'no-store'});
    if (!response.ok) {const error = new Error(await response.text()); error.status = response.status; throw error;}
    return response.status === 204 ? null : response.json();
  }
  async function execute(command, args = [], options = {}) {
    if (typeof command !== 'string' || !command || !Array.isArray(args) || args.some(arg => typeof arg !== 'string')) throw new TypeError('execute requires a command string and an array of string arguments');
    const requestHeaders = {'Content-Type': 'application/json'};
    if (token) requestHeaders.Authorization = 'Bearer ' + token;
    const response = await fetch('/api/commands', {method: 'POST', headers: requestHeaders, body: JSON.stringify({cwd: options.cwd || 'home', command, args}), cache: 'no-store', signal: options.signal});
    if (!response.ok) {const error = new Error(await response.text()); error.status = response.status; throw error;}
    if (!response.body) throw new Error('Command output stream is unavailable.');
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffered = ''; let result = null;
    const consume = line => {
      if (!line) return;
      const event = JSON.parse(line);
      if (event.type === 'output') options.onOutput?.(event);
      if (event.type === 'exit') result = event;
    };
    while (true) {
      const {value, done} = await reader.read();
      buffered += decoder.decode(value || new Uint8Array(), {stream: !done});
      const lines = buffered.split('\n'); buffered = lines.pop();
      for (const line of lines) consume(line);
      if (done) break;
    }
    consume(buffered);
    if (!result) throw new Error('Command ended without an exit event.');
    if (result.code !== 0) {const error = new Error(result.error || `Command exited with code ${result.code}`); error.result = result; throw error;}
    return result;
  }
  function registerAction({label, detail, run}) {
    if (typeof label !== 'string' || !label || typeof run !== 'function') throw new TypeError('registerAction requires label and run');
    const action = button(label, detail || '', async () => {action.disabled = true; try {await run();} finally {action.disabled = false;}});
    $('extension-actions').append(action); $('extensions').hidden = false;
    return () => {action.remove(); if (!$('extension-actions').children.length) $('extensions').hidden = true;};
  }
  const browserAPI = Object.freeze({
    execute,
    registerAction,
    notice,
    getState: () => JSON.parse(JSON.stringify(state)),
    onState: listener => {if (typeof listener !== 'function') throw new TypeError('onState requires a function'); stateListeners.add(listener); return () => stateListeners.delete(listener);}
  });
  Object.defineProperty(window, 'omo', {value: browserAPI, configurable: false, writable: false});
  async function loadExtensions() {
    const extensions = await api('extensions');
    for (const extension of extensions) {
      await new Promise((resolve, reject) => {
        const script = document.createElement('script'); script.src = extension.javascript; script.async = false;
        script.onload = resolve; script.onerror = () => reject(new Error(`Failed to load supervisor extension ${extension.plugin}.`));
        document.head.append(script);
      });
      window.dispatchEvent(new CustomEvent('omo:on_supervisor_load', {detail: Object.freeze({plugin: extension.plugin, files: Object.freeze(extension.files), ...browserAPI})}));
    }
  }
  function select(instance) {
    selected = instance;
    $('empty').hidden = true;
    for (const [id, entry] of terminals) entry.element.hidden = id !== instance.id;
    let entry = terminals.get(instance.id);
    if (!entry) {
      const element = document.createElement('div'); element.className = 'terminal'; $('terminals').append(element);
      const term = new Terminal({cursorBlink: true, fontSize: 14, scrollback: 2000, theme: {background: '#0e1118', foreground: '#e0e5ee'}, allowProposedApi: false});
      const fit = new FitAddon.FitAddon(); term.loadAddon(fit); term.open(element);
      const protocols = token ? ['omo', 'omo-token.' + token] : ['omo'];
      const socket = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/instances/${instance.id}/terminal`, protocols);
      socket.binaryType = 'arraybuffer';
      entry = {element, term, fit, socket}; terminals.set(instance.id, entry);
      term.onData(data => {if (socket.readyState === WebSocket.OPEN) socket.send(new TextEncoder().encode(data));});
      term.onResize(({rows, cols}) => {if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({rows, cols}));});
      socket.onopen = () => {fit.fit(); socket.send(JSON.stringify({rows: term.rows, cols: term.cols}));};
      socket.onmessage = event => {if (event.data instanceof ArrayBuffer) term.write(new Uint8Array(event.data));};
      socket.onclose = () => {if (selected?.id === instance.id) notice('Terminal disconnected. Select it again to reconnect; its process may still be running.'); entry.disconnected = true;};
      socket.onerror = () => notice('Unable to connect to this terminal.');
    } else if (entry.disconnected && instance.state === 'running') {
      entry.socket.close(); entry.term.dispose(); entry.element.remove(); terminals.delete(instance.id); select(instance); return;
    }
    entry.element.hidden = false; entry.fit.fit(); entry.term.focus();
    updateControls(); renderLists();
  }
  function updateControls() {
    $('selected').textContent = selected ? `${selected.mode === 'omo' ? 'Office' : 'Shell'} · ${selected.path}` : 'Select a project to start omo';
    $('shell').disabled = !selected;
    $('estop').disabled = !selected || selected.state !== 'running' || selected.mode !== 'omo';
    $('kill').disabled = !selected || selected.state !== 'running';
    $('remove').disabled = !selected || selected.state !== 'exited';
  }
  function button(label, detail, action, active = false) {
    const b = document.createElement('button'); b.className = 'entry' + (active ? ' active' : ''); b.textContent = label;
    if (detail) {const small = document.createElement('small'); small.textContent = detail; b.append(small);}
    b.onclick = () => Promise.resolve(action()).catch(error => notice(error.message));
    return b;
  }
  function renderLists() {
    $('projects').replaceChildren(...state.projects.map(p => {
      const b = button(p.name, p.available ? p.path : 'Unavailable · ' + p.path, () => launch(p.path, 'omo'));
      b.disabled = !p.available; return b;
    }));
    $('instances').replaceChildren(...state.instances.map(i => button(`${i.mode === 'omo' ? 'Office' : 'Shell'} · ${i.path.split(/[\\/]/).pop()}`, i.state + (i.error ? ' · ' + i.error : ''), () => {notice(''); select(i);}, selected?.id === i.id)));
  }
  async function refresh() {
    state = await api('state');
    state.instances.sort((a, b) => a.started.localeCompare(b.started));
    $('capacity').textContent = `${state.agents} / ${state.max_agents} agents active`;
    if (selected) selected = state.instances.find(i => i.id === selected.id) || selected;
    updateControls(); renderLists();
    for (const listener of stateListeners) {try {listener(browserAPI.getState());} catch (error) {console.error(error);}}
  }
  async function launch(path, mode) {
    if (mode === 'omo' && !confirm(`Start this office?\n\n${path}`)) return;
    const request = {path, mode};
    if (mode === 'omo') request.confirmed = true;
    const instance = await api('instances', 'POST', request); notice(''); await refresh(); select(instance);
  }
  $('shell').onclick = () => launch(selected.path, 'shell').catch(error => notice(error.message));
  $('home-shell').onclick = () => launch('', 'shell').catch(error => notice(error.message));
  $('estop').onclick = () => api(`instances/${selected.id}/estop`, 'POST').then(() => notice('Estop requested. The office is cleaning up its agents.')).catch(error => notice(error.message));
  $('kill').onclick = () => {if (confirm('Force kill this terminal and its child processes? Unfinished work may need recovery.')) api(`instances/${selected.id}/kill`, 'POST').then(refresh).catch(error => notice(error.message));};
  $('remove').onclick = async () => {try {await api(`instances/${selected.id}`, 'DELETE'); const entry = terminals.get(selected.id); if (entry) {entry.socket.close(); entry.term.dispose(); entry.element.remove(); terminals.delete(selected.id);} selected = null; $('empty').hidden = false; await refresh();} catch (error) {notice(error.message);}};
  $('add-project').onclick = () => $('project-dialog').showModal();
  $('cancel-project').onclick = () => $('project-dialog').close();
  $('action').onchange = () => {$('source-label').hidden = $('action').value !== 'clone'; $('save-project').textContent = $('action').value === 'trust' ? 'Trust and load' : 'Create and trust';};
  $('project-form').onsubmit = async event => {
    event.preventDefault(); $('save-project').disabled = true; $('project-error').textContent = '';
    try {await api('projects', 'POST', {action: $('action').value, path: $('project-path').value, source: $('action').value === 'clone' ? $('project-source').value : ''}); $('project-dialog').close(); await refresh();}
    catch (error) {$('project-error').textContent = error.message;}
    finally {$('save-project').disabled = false;}
  };
  new ResizeObserver(() => {if (selected) terminals.get(selected.id)?.fit.fit();}).observe($('terminals'));
  refresh().then(loadExtensions).catch(showAPIError);
  setInterval(() => refresh().catch(showAPIError), 2000);
})();
