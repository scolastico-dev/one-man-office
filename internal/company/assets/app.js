'use strict';
(() => {
  const token = location.hash.slice(1);
  history.replaceState(null, '', location.pathname);
  const $ = id => document.getElementById(id);
  const terminals = new Map();
  let selected = null;
  let state = {projects: [], instances: []};
  const notice = text => { $('notice').textContent = text; };
  const showAPIError = error => notice(!token && error.status === 401 ? 'Open the access URL printed by omo company. The access key stays in this page’s memory; reload using that original URL.' : error.message);
  async function api(path, method = 'GET', body) {
    const headers = {'Content-Type': 'application/json'};
    if (token) headers.Authorization = 'Bearer ' + token;
    const response = await fetch('/api/' + path, {method, headers, body: body === undefined ? undefined : JSON.stringify(body), cache: 'no-store'});
    if (!response.ok) {const error = new Error(await response.text()); error.status = response.status; throw error;}
    return response.status === 204 ? null : response.json();
  }
  async function execute(command, args = [], options = {}) {
    if (typeof command !== 'string' || !command || !Array.isArray(args) || args.some(arg => typeof arg !== 'string')) throw new TypeError('execute requires a command string and an array of string arguments');
    const request = {cwd: options.cwd || 'home', command, args};
    const hasStdin = options.stdin !== undefined;
    let body = JSON.stringify(request);
    const requestHeaders = {'Content-Type': 'application/json'};
    if (hasStdin) {
      let stdin = options.stdin;
      if (typeof stdin === 'string') {
        // FormData accepts strings directly and preserves their text bytes.
      } else if (stdin instanceof Uint8Array) {
        stdin = new Blob([stdin]);
      } else if (!(stdin instanceof Blob)) {
        throw new TypeError('execute stdin must be a string, Uint8Array, Blob, or File');
      }
      const form = new FormData();
      form.append('request', new Blob([body], {type: 'application/json'}));
      form.append('stdin', stdin);
      body = form;
      delete requestHeaders['Content-Type'];
    }
    if (token) requestHeaders.Authorization = 'Bearer ' + token;
    const response = await fetch('/api/commands', {method: 'POST', headers: requestHeaders, body, cache: 'no-store', signal: options.signal});
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
  const ids = Object.freeze({sidebar: 'supervisor-sidebar', main: 'supervisor-main', toolbar: 'supervisor-toolbar', status: 'notice', terminals: 'terminals'});
  const onLoad = (pluginName, listener) => {
    if (typeof pluginName !== 'string' || !pluginName || typeof listener !== 'function') throw new TypeError('onLoad requires a plugin name and function');
    const handleEvent = event => {if (event.detail?.plugin === pluginName) listener(event);};
    window.addEventListener('omo:company_load', handleEvent);
    return () => window.removeEventListener('omo:company_load', handleEvent);
  };
  const dialogElement = $('dialog');
  const dialogTitle = $('dialog-title');
  const dialogMessage = $('dialog-message');
  const dialogInputLabel = $('dialog-input-label');
  const dialogInput = $('dialog-input');
  const dialogCancel = $('dialog-cancel');
  const dialogConfirm = $('dialog-confirm');
  const dialogQueue = [];
  let activeDialog = null;
  const dialogFocusable = () => [...dialogElement.querySelectorAll('button, input, select, textarea, a')]
    .filter(element => !element.disabled && !element.hidden && element.getAttribute('aria-hidden') !== 'true' && element.tabIndex >= 0);
  const restoreDialogFocus = element => {
    if (element && element.isConnected !== false && !element.disabled && !element.hidden && typeof element.focus === 'function') element.focus();
  };
  const dialogCancelValue = kind => kind === 'prompt' ? null : kind === 'confirm' ? false : undefined;
  const settleDialog = value => {
    const request = activeDialog;
    if (!request || request.settled) return;
    request.settled = true;
    activeDialog = null;
    if (dialogElement.open) dialogElement.close();
    restoreDialogFocus(request.invoker);
    request.resolve(value);
    Promise.resolve().then(openNextDialog);
  };
  const openNextDialog = () => {
    if (activeDialog || !dialogQueue.length) return;
    const request = activeDialog = dialogQueue.shift();
    dialogTitle.textContent = request.kind === 'alert' ? 'Notice' : request.kind === 'prompt' ? 'Input required' : 'Confirm action';
    dialogMessage.textContent = request.message;
    dialogInput.value = request.initialValue;
    dialogInputLabel.hidden = request.kind !== 'prompt';
    dialogInput.hidden = request.kind !== 'prompt';
    dialogCancel.hidden = request.kind === 'alert';
    dialogConfirm.textContent = request.kind === 'alert' ? 'OK' : 'Continue';
    dialogElement.showModal();
    const focusable = dialogFocusable();
    (request.kind === 'prompt' ? dialogInput : focusable[0])?.focus();
  };
  const requestDialog = (kind, message, initialValue = '') => new Promise(resolve => {
    dialogQueue.push({kind, message: String(message), initialValue: String(initialValue), invoker: document.activeElement, resolve, settled: false});
    openNextDialog();
  });
  dialogCancel.onclick = () => settleDialog(dialogCancelValue(activeDialog?.kind));
  dialogConfirm.onclick = () => settleDialog(activeDialog?.kind === 'prompt' ? dialogInput.value : true);
  dialogElement.addEventListener('cancel', event => {event.preventDefault(); settleDialog(dialogCancelValue(activeDialog?.kind));});
  dialogElement.addEventListener('close', () => {if (activeDialog && !activeDialog.settled) settleDialog(dialogCancelValue(activeDialog.kind));});
  dialogElement.addEventListener('keydown', event => {
    if (!activeDialog) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      settleDialog(dialogCancelValue(activeDialog.kind));
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      settleDialog(activeDialog.kind === 'prompt' ? dialogInput.value : true);
      return;
    }
    if (event.key !== 'Tab') return;
    const focusable = dialogFocusable();
    if (!focusable.length) return;
    const index = focusable.indexOf(document.activeElement);
    const nextIndex = event.shiftKey
      ? (index <= 0 ? focusable.length - 1 : index - 1)
      : (index < 0 || index === focusable.length - 1 ? 0 : index + 1);
    event.preventDefault();
    focusable[nextIndex].focus();
  });
  const dialog = Object.freeze({
    alert: message => requestDialog('alert', message),
    confirm: message => requestDialog('confirm', message),
    prompt: (message, initialValue = '') => requestDialog('prompt', message, initialValue),
  });
  const browserAPI = Object.freeze({execute, $, ids, onLoad, token, dialog});
  Object.defineProperty(window, 'omo', {value: browserAPI, configurable: false, writable: false});
  const deepFreeze = value => {
    if (!value || typeof value !== 'object' || Object.isFrozen(value)) return value;
    Object.freeze(value);
    for (const child of Object.values(value)) deepFreeze(child);
    return value;
  };
  async function loadExtensions() {
    const extensions = await api('extensions');
    for (const extension of extensions) {
      await new Promise((resolve, reject) => {
        const script = document.createElement('script'); script.src = extension.javascript; script.async = false;
        script.onload = resolve; script.onerror = () => reject(new Error(`Failed to load company extension ${extension.plugin}.`));
        document.head.append(script);
      });
      const detail = deepFreeze({plugin: extension.plugin, config: deepFreeze(extension.config || {})});
      window.dispatchEvent(new CustomEvent('omo:company_load', {detail}));
    }
  }
  function select(instance) {
    selected = instance;
    $('empty').hidden = true;
    for (const [id, entry] of terminals) entry.element.hidden = id !== instance.id;
    let entry = terminals.get(instance.id);
    if (!entry) {
      const element = document.createElement('div'); element.className = 'terminal'; $('terminals').append(element);
      const term = new Terminal({cursorBlink: true, fontSize: 14, scrollback: 2000, fontFamily: '"Cascadia Code", "SFMono-Regular", Consolas, "Liberation Mono", monospace', theme: {background: '#141414', foreground: '#d0ced3', cursor: '#bb9add', selectionBackground: '#51405f'}, allowProposedApi: false});
      const fit = new FitAddon.FitAddon(); term.loadAddon(fit); term.open(element);
      const protocols = token ? ['omo', 'omo-token.' + token] : ['omo'];
      const socket = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/instances/${instance.id}/terminal`, protocols);
      socket.binaryType = 'arraybuffer';
      const input = new TerminalInput(socket, message => {if (selected?.id === instance.id) notice(message);});
      entry = {element, term, fit, socket, input}; terminals.set(instance.id, entry);
      term.onData(data => input.send(data));
      term.onResize(({rows, cols}) => {if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({rows, cols}));});
      socket.onopen = () => {fit.fit(); socket.send(JSON.stringify({rows: term.rows, cols: term.cols})); input.flush();};
      socket.onmessage = event => {
        if (event.data instanceof ArrayBuffer) term.write(new Uint8Array(event.data));
        else if (JSON.parse(event.data).type === 'input-ack') input.acknowledge();
      };
      socket.onclose = () => {input.close(); if (selected?.id === instance.id) notice('Terminal disconnected. Select it again to reconnect; its process may still be running.'); entry.disconnected = true;};
      socket.onerror = () => notice('Unable to connect to this terminal.');
    } else if (entry.disconnected && instance.state === 'running') {
      entry.input.close(); entry.socket.close(); entry.term.dispose(); entry.element.remove(); terminals.delete(instance.id); select(instance); return;
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
  function renderList(id, items, emptyText) {
    const list = $(id);
    // Reuse terminal buttons so polling preserves keyboard focus and hover transitions.
    const existing = new Map([...list.querySelectorAll('.entry')].map(b => [b.dataset.key, b]));
    const keep = new Set();
    items.forEach((item, index) => {
      let b = existing.get(item.key);
      if (!b) {
        b = document.createElement('button');
        b.append(document.createElement('span'), document.createElement('small'));
        b.dataset.key = item.key;
      }
      b.className = 'entry' + (item.active ? ' active' : '');
      b.firstElementChild.textContent = item.label;
      b.lastElementChild.textContent = item.detail;
      b.disabled = item.disabled || false;
      b.title = item.title || item.detail;
      b.dataset.state = item.state || '';
      if (item.active) b.setAttribute('aria-current', 'true');
      else b.removeAttribute('aria-current');
      b.onclick = () => Promise.resolve(item.action()).catch(error => notice(error.message));
      keep.add(b);
      if (list.children[index] !== b) list.insertBefore(b, list.children[index] || null);
    });
    for (const child of [...list.children]) if (!keep.has(child)) child.remove();
    if (!items.length) {
      const empty = document.createElement('p'); empty.className = 'list-empty'; empty.textContent = emptyText; list.append(empty);
    }
  }
  async function removeProject(path) {
    try {
      if (!await dialog.confirm(`Remove this office from the trust list?\n\n${path}\n\nThis only removes trust. No files or directories will be deleted.`)) return;
      await api('projects', 'POST', {action: 'untrust', path});
      notice('Office removed from the trust list. No files were deleted.');
      await refresh();
    } catch (error) {
      notice(error.message);
    }
  }
  function renderProjects() {
    const list = $('projects');
    const existing = new Map([...list.querySelectorAll('.project-row')].map(row => [row.dataset.key, row]));
    const keep = new Set();
    state.projects.forEach((project, index) => {
      let row = existing.get(project.path);
      if (!row) {
        row = document.createElement('div');
        row.append(document.createElement('button'), document.createElement('button'));
        row.children[0].append(document.createElement('span'), document.createElement('small'));
      }
      const launchButton = row.children[0];
      const removeButton = row.children[1];
      row.className = 'project-row';
      row.dataset.key = project.path;
      row.setAttribute('role', 'listitem');
      launchButton.className = 'entry project-launch';
      launchButton.type = 'button';
      launchButton.firstElementChild.textContent = project.name;
      launchButton.lastElementChild.textContent = project.available ? project.path : 'Unavailable · ' + project.path;
      launchButton.disabled = !project.available;
      launchButton.title = project.path;
      launchButton.onclick = () => launch(project.path, 'omo').catch(error => notice(error.message));
      removeButton.className = 'project-remove';
      removeButton.type = 'button';
      removeButton.textContent = 'Remove';
      removeButton.setAttribute('aria-label', `Remove ${project.name} from the trust list`);
      removeButton.onclick = () => removeProject(project.path);
      keep.add(row);
      if (list.children[index] !== row) list.insertBefore(row, list.children[index] || null);
    });
    for (const child of [...list.children]) if (!keep.has(child)) child.remove();
    if (!state.projects.length) {
      const empty = document.createElement('p');
      empty.className = 'list-empty';
      empty.textContent = 'No offices to launch. Add a project to get started.';
      list.append(empty);
    }
  }
  function renderLists() {
    $('project-count').textContent = state.projects.length;
    $('instance-count').textContent = state.instances.length;
    renderProjects();
    renderList('instances', state.instances.map(i => ({
      key: i.id, label: `${i.mode === 'omo' ? 'Office' : 'Shell'} · ${i.path.split(/[\\/]/).pop() || i.path}`,
      detail: i.state + (i.error ? ' · ' + i.error : ''), title: i.path, state: i.state,
      active: selected?.id === i.id, action: () => {notice(''); select(i);},
    })), 'No terminals yet. Launch an office or open a shell.');
  }
  async function refresh() {
    state = await api('state');
    state.instances.sort((a, b) => a.started.localeCompare(b.started));
    $('capacity').textContent = `${state.agents} / ${state.max_agents} agents active`;
    $('capacity-meter').max = state.max_agents;
    $('capacity-meter').value = state.agents;
    if (selected) selected = state.instances.find(i => i.id === selected.id) || selected;
    updateControls(); renderLists();
  }
  async function launch(path, mode) {
    if (mode === 'omo' && !await dialog.confirm(`Start this office?\n\n${path}`)) return;
    const request = {path, mode};
    if (mode === 'omo') request.confirmed = true;
    const instance = await api('instances', 'POST', request); notice(''); await refresh(); select(instance);
  }
  $('shell').onclick = () => launch(selected.path, 'shell').catch(error => notice(error.message));
  $('home-shell').onclick = () => launch('', 'shell').catch(error => notice(error.message));
  $('estop').onclick = () => api(`instances/${selected.id}/estop`, 'POST').then(() => notice('Estop requested. The office is cleaning up its agents.')).catch(error => notice(error.message));
  $('kill').onclick = async () => {if (await dialog.confirm('Force kill this terminal and its child processes? Unfinished work may need recovery.')) api(`instances/${selected.id}/kill`, 'POST').then(refresh).catch(error => notice(error.message));};
  $('remove').onclick = async () => {try {await api(`instances/${selected.id}`, 'DELETE'); const entry = terminals.get(selected.id); if (entry) {entry.input.close(); entry.socket.close(); entry.term.dispose(); entry.element.remove(); terminals.delete(selected.id);} selected = null; $('empty').hidden = false; await refresh();} catch (error) {notice(error.message);}};
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
