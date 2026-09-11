'use strict';
(() => {
  const token = location.hash.slice(1);
  history.replaceState(null, '', location.pathname);
  const $ = id => document.getElementById(id);
  const terminals = new Map();
  let selected = null;
  let state = {projects: [], instances: []};
  let editingProjects = false;
  const expandedInstances = new Map();
  let officesPanelExpanded = true;
  let triggerMenuOpen = false;
  const notice = text => { $('notice').textContent = text; };
  const showAPIError = error => notice(!token && error.status === 401 ? 'Open the access URL printed by omo company. The access key stays in this page’s memory; reload using that original URL.' : error.message);
  async function api(path, method = 'GET', body) {
    const headers = {'Content-Type': 'application/json'};
    if (token) headers.Authorization = 'Bearer ' + token;
    const response = await fetch('/api/' + path, {method, headers, body: body === undefined ? undefined : JSON.stringify(body), cache: 'no-store'});
    if (!response.ok) {const error = new Error(await response.text()); error.status = response.status; throw error;}
    return response.status === 204 ? null : response.json();
  }
  const triggerFor = pluginName => async (office, action, args = []) => {
    if (office !== null && (typeof office !== 'string' || !office)) throw new TypeError('trigger office must be null or a non-empty instance ID');
    if (typeof pluginName !== 'string' || !pluginName) throw new TypeError('trigger is only available to a company-load plugin');
    if (typeof action !== 'string' || !action || /[\0\r\n]/.test(action)) throw new TypeError('trigger action must be a non-empty string without control characters');
    if (!Array.isArray(args) || args.some(arg => typeof arg !== 'string')) throw new TypeError('trigger args must be an array of strings');
    const path = office === null ? `plugins/${encodeURIComponent(pluginName)}/trigger` : `instances/${encodeURIComponent(office)}/trigger`;
    const body = office === null ? {action, args} : {plugin: pluginName, action, args};
    const headers = {'Content-Type': 'application/json'};
    if (token) headers.Authorization = 'Bearer ' + token;
    const response = await fetch('/api/' + path, {method: 'POST', headers, body: JSON.stringify(body), cache: 'no-store'});
    if (!response.ok) throw new Error(await response.text());
    return response.json();
  };
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
  const pendingCompanyLoads = new WeakMap();
  const onLoad = (pluginName, listener) => {
    if (typeof pluginName !== 'string' || !pluginName || typeof listener !== 'function') throw new TypeError('onLoad requires a plugin name and function');
    const handleEvent = event => {
      if (event.detail?.plugin !== pluginName) return;
      const result = listener(event);
      const pending = pendingCompanyLoads.get(event);
      if (pending) pending.push(Promise.resolve(result));
    };
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
  const browserAPI = Object.freeze({execute, $, ids, onLoad, token, dialog, trigger: triggerFor('')});
  let activeExtensionAPI = null;
  const scopedAPI = plugin => Object.freeze({...browserAPI, trigger: triggerFor(plugin)});
  Object.defineProperty(window, 'omo', {get: () => activeExtensionAPI || browserAPI, configurable: false});
  const deepFreeze = value => {
    if (!value || typeof value !== 'object' || Object.isFrozen(value)) return value;
    Object.freeze(value);
    for (const child of Object.values(value)) deepFreeze(child);
    return value;
  };
  async function loadExtensions() {
    const extensions = await api('extensions');
    for (const extension of extensions) {
      activeExtensionAPI = scopedAPI(extension.plugin);
      try {
        await new Promise((resolve, reject) => {
          const script = document.createElement('script'); script.src = extension.javascript; script.async = false;
          script.onload = resolve; script.onerror = () => reject(new Error(`Failed to load company extension ${extension.plugin}.`));
          document.head.append(script);
        });
        const detail = deepFreeze({plugin: extension.plugin, config: deepFreeze(extension.config || {})});
        const event = new CustomEvent('omo:company_load', {detail});
        const pending = [];
        pendingCompanyLoads.set(event, pending);
        try {
          window.dispatchEvent(event);
          await Promise.all(pending);
        } finally {
          pendingCompanyLoads.delete(event);
        }
      } finally {
        activeExtensionAPI = null;
      }
    }
  }
  const mediaMatches = query => typeof window.matchMedia === 'function' && window.matchMedia(query).matches;
  const isRunnableOffice = instance => instance?.mode === 'omo' && instance.state === 'running';
  const isNarrowViewport = () => mediaMatches('(max-width: 650px)');
  const SIDEBAR_STORAGE_KEY = 'omo.sidebarWidth';
  const SIDEBAR_MIN_WIDTH = 220;
  const SIDEBAR_MAX_WIDTH = 600;
  const sidebarResizer = $('sidebar-resizer');
  const sidebar = $('supervisor-sidebar');
  const sidebarMaxWidth = () => {
    const viewportWidth = Number(window.innerWidth);
    const viewportMax = Number.isFinite(viewportWidth) ? Math.floor(viewportWidth * 0.6) : SIDEBAR_MAX_WIDTH;
    return Math.max(SIDEBAR_MIN_WIDTH, Math.min(SIDEBAR_MAX_WIDTH, viewportMax));
  };
  const clampSidebarWidth = value => {
    const numeric = Number(value);
    const fallback = Number(window.innerWidth) <= 1100 ? 250 : 290;
    const width = Number.isFinite(numeric) ? numeric : fallback;
    return Math.round(Math.max(SIDEBAR_MIN_WIDTH, Math.min(sidebarMaxWidth(), width)));
  };
  const persistSidebarWidth = width => {
    try { window.localStorage.setItem(SIDEBAR_STORAGE_KEY, String(width)); } catch {}
  };
  const applySidebarWidth = (value, persist = false) => {
    const width = clampSidebarWidth(value);
    if (persist) document.body.dataset.sidebarWidthUserSet = 'true';
    document.documentElement.style.setProperty('--sidebar-width', `${width}px`);
    sidebarResizer.setAttribute('aria-valuemax', String(sidebarMaxWidth()));
    sidebarResizer.setAttribute('aria-valuenow', String(width));
    if (persist) persistSidebarWidth(width);
    return width;
  };
  let storedSidebarWidth;
  try {
    const raw = window.localStorage.getItem(SIDEBAR_STORAGE_KEY);
    if (typeof raw === 'string' && raw.trim() !== '') {
      const numeric = Number(raw);
      if (Number.isFinite(numeric)) storedSidebarWidth = numeric;
    }
  } catch {}
  if (storedSidebarWidth === undefined) delete document.body.dataset.sidebarWidthUserSet;
  else document.body.dataset.sidebarWidthUserSet = 'true';
  applySidebarWidth(storedSidebarWidth === undefined ? (Number(window.innerWidth) <= 1100 ? 250 : 290) : storedSidebarWidth);
  let activeSidebarPointer = null;
  let sidebarPointerStartX = 0;
  let sidebarPointerStartWidth = 0;
  sidebarResizer.addEventListener('pointerdown', event => {
    if (isNarrowViewport() || event.button !== 0 || activeSidebarPointer !== null) return;
    const pointerId = event.pointerId;
    const clientX = Number(event.clientX);
    if (!Number.isFinite(clientX)) return;
    const geometry = sidebar.getBoundingClientRect();
    activeSidebarPointer = pointerId;
    sidebarPointerStartX = clientX;
    sidebarPointerStartWidth = clampSidebarWidth(geometry.width);
    if (typeof sidebarResizer.setPointerCapture === 'function') sidebarResizer.setPointerCapture(pointerId);
  });
  sidebarResizer.addEventListener('pointermove', event => {
    if (isNarrowViewport() || activeSidebarPointer === null || event.pointerId !== activeSidebarPointer) return;
    const clientX = Number(event.clientX);
    if (Number.isFinite(clientX)) applySidebarWidth(sidebarPointerStartWidth + clientX - sidebarPointerStartX);
  });
  const finishSidebarPointer = event => {
    if (activeSidebarPointer === null || event.pointerId !== activeSidebarPointer) return;
    const pointerId = activeSidebarPointer;
    activeSidebarPointer = null;
    if (typeof sidebarResizer.hasPointerCapture !== 'function' || sidebarResizer.hasPointerCapture(pointerId)) {
      if (typeof sidebarResizer.releasePointerCapture === 'function') sidebarResizer.releasePointerCapture(pointerId);
    }
    applySidebarWidth(sidebarResizer.getAttribute('aria-valuenow'), true);
  };
  sidebarResizer.addEventListener('pointerup', finishSidebarPointer);
  sidebarResizer.addEventListener('pointercancel', finishSidebarPointer);
  sidebarResizer.addEventListener('keydown', event => {
    if (isNarrowViewport()) return;
    const offset = event.key === 'ArrowLeft' ? -16 : event.key === 'ArrowRight' ? 16 : 0;
    if (!offset) return;
    event.preventDefault();
    applySidebarWidth(Number(sidebarResizer.getAttribute('aria-valuenow')) + offset, true);
  });
  const splitTriggerArgs = input => {
    const args = [];
    let current = '';
    let quote = null;
    let escaped = false;
    let started = false;
    for (const character of String(input)) {
      if (escaped) {
        current += character;
        escaped = false;
        started = true;
        continue;
      }
      if (character === '\\') {
        escaped = true;
        started = true;
        continue;
      }
      if (quote) {
        if (character === quote) quote = null;
        else current += character;
        started = true;
        continue;
      }
      if (character === "'" || character === '"') {
        quote = character;
        started = true;
      } else if (/\s/.test(character)) {
        if (started) {
          args.push(current);
          current = '';
          started = false;
        }
      } else {
        current += character;
        started = true;
      }
    }
    if (quote) throw new Error('Unmatched quote in trigger arguments.');
    if (started || escaped) args.push(current + (escaped ? '\\' : ''));
    return args;
  };
  const withinTriggerMenu = target => {
    const button = $('triggers');
    const menu = $('trigger-menu');
    for (let node = target; node; node = node.parentNode) if (node === button || node === menu) return true;
    return false;
  };
  function closeTriggerMenu(restoreFocus = false) {
    triggerMenuOpen = false;
    const button = $('triggers');
    const menu = $('trigger-menu');
    menu.hidden = true;
    button.setAttribute('aria-expanded', 'false');
    if (restoreFocus && !button.disabled) button.focus();
  }
  function openTriggerMenu() {
    const button = $('triggers');
    if (button.disabled) return;
    triggerMenuOpen = true;
    $('trigger-menu').hidden = false;
    button.setAttribute('aria-expanded', 'true');
  }
  async function runTriggerAction(action) {
    closeTriggerMenu(true);
    let args = [];
    if (action.args === true) {
      const input = await dialog.prompt(`Arguments for ${action.plugin} ${action.action}`);
      if (input === null) return;
      try {
        args = splitTriggerArgs(input);
      } catch (error) {
        notice(error.message);
        return;
      }
    }
    try {
      const response = await api(`instances/${encodeURIComponent(selected.id)}/trigger`, 'POST', {plugin: action.plugin, action: action.action, args});
      notice(`Triggered ${action.plugin} ${action.action} (request ${response.request_id})`);
    } catch (error) {
      notice(error.message);
    }
  }
  function renderTriggers() {
    const button = $('triggers');
    const menu = $('trigger-menu');
    const actions = Array.isArray(selected?.actions) ? selected.actions.filter(action => action && typeof action.plugin === 'string' && typeof action.action === 'string') : [];
    const runnable = isRunnableOffice(selected);
    button.disabled = !runnable || !actions.length;
    button.title = !selected
      ? 'Select a running office with available actions.'
      : !runnable
        ? 'Triggers require a running office terminal.'
        : !actions.length
          ? 'No triggers are available for this office.'
          : 'Run an action in the selected office.';
    if (button.disabled) closeTriggerMenu(false);
    const existing = new Map([...menu.querySelectorAll('.trigger-action')].map(item => [item.dataset.key, item]));
    const keep = new Set();
    actions.forEach((action, index) => {
      const key = `${action.plugin}:${action.action}`;
      let item = existing.get(key);
      if (!item) {
        item = document.createElement('button');
        item.append(document.createElement('span'));
      }
      item.className = 'trigger-action';
      item.type = 'button';
      item.dataset.key = key;
      item.setAttribute('role', 'menuitem');
      item.firstElementChild.textContent = `${action.plugin} · ${action.action} — ${action.description || ''}`;
      item.onclick = () => runTriggerAction(action);
      keep.add(item);
      if (menu.children[index] !== item) menu.insertBefore(item, menu.children[index] || null);
    });
    for (const child of [...menu.children]) if (!keep.has(child)) child.remove();
  }
  function select(instance) {
    closeTriggerMenu(false);
    selected = instance;
    $('empty').hidden = true;
    for (const [id, entry] of terminals) entry.element.hidden = id !== instance.id;
    let entry = terminals.get(instance.id);
    if (!entry) {
      const element = document.createElement('div'); element.className = 'terminal'; $('terminals').append(element);
      const term = new Terminal({cursorBlink: true, fontSize: 14, scrollback: instance.mode === 'shell' ? 2000 : 0, fontFamily: '"Cascadia Code", "SFMono-Regular", Consolas, "Liberation Mono", monospace', theme: {background: '#141414', foreground: '#d0ced3', cursor: '#bb9add', selectionBackground: '#51405f'}, allowProposedApi: false});
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
    $('selected').textContent = selected ? `${selected.mode === 'omo' ? 'Office' : selected.mode === 'setup' ? 'Setup' : 'Shell'} · ${selected.path}` : 'Select a project to start omo';
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
    const activeControl = document.activeElement;
    const activeRow = activeControl?.parentNode;
    const activeKey = activeRow?.className === 'project-row' ? activeRow.dataset.key : null;
    const activeControlIndex = activeKey ? [...activeRow.children].indexOf(activeControl) : -1;
    const existing = new Map([...list.querySelectorAll('.project-row')].map(row => [row.dataset.key, row]));
    const keep = new Set();
    state.projects.forEach((project, index) => {
      let row = existing.get(project.path);
      if (!row) {
        row = document.createElement('div');
        const launchButton = document.createElement('button');
        launchButton.append(document.createElement('span'), document.createElement('small'));
        row.append(launchButton);
      }
      const launchButton = row.children[0];
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
      let [upButton, downButton, removeButton] = row.projectControls || [];
      if (!upButton) {
        upButton = document.createElement('button');
        downButton = document.createElement('button');
        removeButton = document.createElement('button');
        row.projectControls = [upButton, downButton, removeButton];
      }
      upButton.className = 'project-move project-up';
      upButton.type = 'button';
      upButton.textContent = '↑';
      upButton.disabled = index === 0;
      upButton.setAttribute('aria-label', `Move ${project.name} up`);
      upButton.onclick = () => moveProject(index, -1);
      downButton.className = 'project-move project-down';
      downButton.type = 'button';
      downButton.textContent = '↓';
      downButton.disabled = index === state.projects.length - 1;
      downButton.setAttribute('aria-label', `Move ${project.name} down`);
      downButton.onclick = () => moveProject(index, 1);
      removeButton.className = 'project-remove';
      removeButton.type = 'button';
      removeButton.textContent = 'Remove';
      removeButton.setAttribute('aria-label', `Remove ${project.name} from the trust list`);
      removeButton.onclick = () => removeProject(project.path);
      if (editingProjects) {
        row.append(upButton, downButton, removeButton);
      } else {
        for (const control of row.projectControls) if (control.parentNode === row) control.remove();
      }
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
    if (activeKey && activeControlIndex >= 0) {
      const row = [...list.querySelectorAll('.project-row')].find(candidate => candidate.dataset.key === activeKey);
      const control = row?.children[activeControlIndex];
      if (control && typeof control.focus === 'function') control.focus();
    }
  }
  function renderInstances() {
    const list = $('instances');
    const existing = new Map([...list.querySelectorAll('.instance-node')].map(row => [row.dataset.key, row]));
    const keep = new Set();
    state.instances.forEach((instance, index) => {
      let row = existing.get(instance.id);
      if (!row) {
        row = document.createElement('div');
        const officeButton = document.createElement('button');
        officeButton.append(document.createElement('span'), document.createElement('small'));
        const toggle = document.createElement('button');
        const agentList = document.createElement('div');
        row.append(officeButton, toggle, agentList);
        row.officeButton = officeButton;
        row.toggle = toggle;
        row.agentList = agentList;
      }
      const officeButton = row.officeButton;
      const toggle = row.toggle;
      const agentList = row.agentList;
      const agents = isRunnableOffice(instance) && Array.isArray(instance.agents) ? instance.agents : [];
      const canExpand = agents.length > 0;
      if (!expandedInstances.has(instance.id)) expandedInstances.set(instance.id, !isNarrowViewport());
      const expanded = Boolean(expandedInstances.get(instance.id) && canExpand);
      const selectedInstance = selected?.id === instance.id;
      const peekName = instance.tui?.mode === 'peek' ? instance.tui?.peek : '';
      const visiblePeek = selectedInstance && expanded && Boolean(peekName) && agents.some(agent => agent.name === peekName);
      const officeActive = selectedInstance && !visiblePeek;
      row.className = 'instance-node';
      row.dataset.key = instance.id;
      row.setAttribute('role', 'treeitem');
      officeButton.className = 'entry instance-entry' + (officeActive ? ' active' : '');
      officeButton.type = 'button';
      officeButton.dataset.key = instance.id;
      officeButton.firstElementChild.textContent = `${instance.mode === 'omo' ? 'Office' : instance.mode === 'setup' ? 'Setup' : 'Shell'} · ${instance.path.split(/[\\/]/).pop() || instance.path}`;
      officeButton.lastElementChild.textContent = instance.state + (instance.error ? ' · ' + instance.error : '');
      officeButton.title = instance.path;
      officeButton.dataset.state = instance.state || '';
      if (officeActive) officeButton.setAttribute('aria-current', 'true');
      else officeButton.removeAttribute('aria-current');
      officeButton.onclick = async () => {
        notice('');
        select(instance);
        if (isRunnableOffice(instance) && instance.tui?.mode === 'peek') await api(`instances/${encodeURIComponent(instance.id)}/tui`, 'POST', {agent: ''});
      };
      const officeClick = officeButton.onclick;
      officeButton.onclick = () => {
        try {
          return Promise.resolve(officeClick()).catch(error => notice(error.message));
        } catch (error) {
          notice(error.message);
        }
      };
      toggle.className = 'instance-toggle';
      toggle.type = 'button';
      toggle.textContent = expanded ? '⌄' : '›';
      toggle.hidden = !canExpand;
      toggle.setAttribute('aria-label', `${expanded ? 'Collapse' : 'Expand'} agents for ${instance.path}`);
      toggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
      toggle.onclick = () => {
        expandedInstances.set(instance.id, !expandedInstances.get(instance.id));
        renderInstances();
      };
      agentList.className = 'agent-list';
      agentList.setAttribute('role', 'group');
      agentList.hidden = !expanded;
      const existingAgents = new Map([...agentList.querySelectorAll('.agent-entry')].map(agent => [agent.dataset.key, agent]));
      const keepAgents = new Set();
      agents.forEach((agent, agentIndex) => {
        const key = agent.name;
        let agentButton = existingAgents.get(key);
        if (!agentButton) {
          agentButton = document.createElement('button');
          agentButton.append(document.createElement('span'), document.createElement('small'));
        }
        const highlighted = visiblePeek && peekName === agent.name;
        agentButton.className = 'entry agent-entry' + (highlighted ? ' active' : '');
        agentButton.type = 'button';
        agentButton.dataset.key = key;
        agentButton.firstElementChild.textContent = agent.name;
        agentButton.lastElementChild.textContent = `${agent.role || 'Agent'} · ${agent.state || 'unknown'}`;
        agentButton.title = agent.step || `${agent.role || 'Agent'} · ${agent.state || 'unknown'}`;
        agentButton.hidden = !expanded;
        if (highlighted) agentButton.setAttribute('aria-current', 'true');
        else agentButton.removeAttribute('aria-current');
        agentButton.setAttribute('aria-label', `${agent.name}, ${agent.role || 'agent'}, ${agent.state || 'unknown'}`);
        agentButton.onclick = async () => {
          notice('');
          select(instance);
          await api(`instances/${encodeURIComponent(instance.id)}/tui`, 'POST', {agent: agent.name});
        };
        const agentClick = agentButton.onclick;
        agentButton.onclick = () => {
          try {
            return Promise.resolve(agentClick()).catch(error => notice(error.message));
          } catch (error) {
            notice(error.message);
          }
        };
        keepAgents.add(agentButton);
        if (agentList.children[agentIndex] !== agentButton) agentList.insertBefore(agentButton, agentList.children[agentIndex] || null);
      });
      for (const child of [...agentList.children]) if (!keepAgents.has(child)) child.remove();
      keep.add(row);
      if (list.children[index] !== row) list.insertBefore(row, list.children[index] || null);
    });
    for (const child of [...list.children]) if (!keep.has(child)) child.remove();
    if (!state.instances.length) {
      const empty = document.createElement('p');
      empty.className = 'list-empty';
      empty.textContent = 'No terminals yet. Launch an office or open a shell.';
      list.append(empty);
    }
  }
  async function moveProject(index, offset) {
    const projects = state.projects.slice();
    const target = index + offset;
    if (target < 0 || target >= projects.length) return;
    [projects[index], projects[target]] = [projects[target], projects[index]];
    try {
      const response = await api('projects', 'POST', {action: 'reorder', paths: projects.map(project => project.path)});
      if (!response || !Array.isArray(response.projects)) throw new Error('Reorder response was invalid.');
      state.projects = response.projects;
      renderLists();
    } catch (error) {
      notice(error.message);
      renderProjects();
    }
  }
  function renderLists() {
    $('project-count').textContent = state.projects.length;
    $('instance-count').textContent = state.instances.length;
    renderProjects();
    renderInstances();
    renderTriggers();
  }
  function renderOfficesPanel() {
    const expanded = officesPanelExpanded;
    const toggle = $('projects-toggle');
    const content = $('projects-panel-content');
    const projects = $('projects');
    const actions = $('sidebar-actions');
    toggle.type = 'button';
    toggle.textContent = expanded ? '⌄' : '›';
    toggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    toggle.setAttribute('aria-controls', 'projects-panel-content');
    toggle.setAttribute('aria-label', `${expanded ? 'Collapse' : 'Expand'} offices`);
    content.hidden = !expanded;
    projects.hidden = !expanded;
    actions.hidden = !expanded;
    $('offices-panel').className = `panel list-panel offices-panel${expanded ? '' : ' offices-collapsed'}`;
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
  $('edit-projects').onclick = () => {editingProjects = !editingProjects; updateProjectEditButton(); renderProjects();};
  $('projects-toggle').onclick = () => {officesPanelExpanded = !officesPanelExpanded; renderOfficesPanel();};
  $('cancel-project').onclick = () => $('project-dialog').close();
  $('action').onchange = () => {$('source-label').hidden = $('action').value !== 'clone'; $('save-project').textContent = $('action').value === 'trust' ? 'Trust and load' : $('action').value === 'create' ? 'Create' : 'Clone';};
  $('project-form').onsubmit = async event => {
    event.preventDefault(); $('save-project').disabled = true; $('project-error').textContent = '';
    try {
      const action = $('action').value;
      const request = {action, path: $('project-path').value};
      if (action === 'clone') request.source = $('project-source').value;
      const instance = await api('projects', 'POST', request);
      $('project-dialog').close(); await refresh(); if (instance?.id) select(instance);
    }
    catch (error) {$('project-error').textContent = error.message;}
    finally {$('save-project').disabled = false;}
  };
  function updateProjectEditButton() {
    $('edit-projects').textContent = editingProjects ? 'Done' : 'Edit';
    $('edit-projects').setAttribute('aria-pressed', editingProjects ? 'true' : 'false');
  }
  const triggerButton = $('triggers');
  const triggerMenu = $('trigger-menu');
  triggerMenu.setAttribute('role', 'menu');
  triggerButton.onclick = () => {
    if (triggerMenuOpen) closeTriggerMenu(true);
    else openTriggerMenu();
  };
  triggerButton.addEventListener('keydown', event => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      openTriggerMenu();
      const items = [...triggerMenu.querySelectorAll('.trigger-action')].filter(item => !item.disabled);
      if (items.length) items[event.key === 'ArrowUp' ? items.length - 1 : 0].focus();
    } else if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      triggerButton.click();
    } else if (event.key === 'Escape' && triggerMenuOpen) {
      event.preventDefault();
      closeTriggerMenu(true);
    }
  });
  triggerMenu.addEventListener('keydown', event => {
    const items = [...triggerMenu.querySelectorAll('.trigger-action')].filter(item => !item.disabled);
    if (event.key === 'Escape') {
      event.preventDefault();
      closeTriggerMenu(true);
      return;
    }
    if ((event.key === 'Enter' || event.key === ' ') && document.activeElement?.className === 'trigger-action') {
      event.preventDefault();
      document.activeElement.click();
      return;
    }
    if (!items.length || (event.key !== 'ArrowDown' && event.key !== 'ArrowUp')) return;
    event.preventDefault();
    const current = items.indexOf(document.activeElement);
    const offset = event.key === 'ArrowDown' ? 1 : -1;
    items[(current + offset + items.length) % items.length].focus();
  });
  document.addEventListener('click', event => {
    if (triggerMenuOpen && !withinTriggerMenu(event.target)) closeTriggerMenu(false);
  });
  updateProjectEditButton();
  renderOfficesPanel();
  new ResizeObserver(() => {if (selected) terminals.get(selected.id)?.fit.fit();}).observe($('terminals'));
  refresh().then(loadExtensions).catch(showAPIError);
  setInterval(() => refresh().catch(showAPIError), 2000);
})();
