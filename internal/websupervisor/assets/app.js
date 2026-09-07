'use strict';
(() => {
  const token = location.hash.slice(1);
  history.replaceState(null, '', location.pathname);
  const $ = id => document.getElementById(id);
  const terminals = new Map();
  let selected = null;
  let state = {projects: [], instances: []};
  const notice = text => { $('notice').textContent = text; };
  const showAPIError = error => notice(!token && error.status === 401 ? 'Open the access URL printed by omo supervisor. The access key stays in this page’s memory; reload using that original URL.' : error.message);
  async function api(path, method = 'GET', body) {
    const response = await fetch('/api/' + path, {method, headers: {Authorization: 'Bearer ' + token, 'Content-Type': 'application/json'}, body: body === undefined ? undefined : JSON.stringify(body), cache: 'no-store'});
    if (!response.ok) {const error = new Error(await response.text()); error.status = response.status; throw error;}
    return response.status === 204 ? null : response.json();
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
      const socket = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/instances/${instance.id}/terminal`, ['omo', 'omo-token.' + token]);
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
  }
  async function launch(path, mode) {
    if (mode === 'omo' && !confirm(`Start this office?\n\n${path}`)) return;
    const request = {path, mode};
    if (mode === 'omo') request.confirmed = true;
    const instance = await api('instances', 'POST', request); notice(''); await refresh(); select(instance);
  }
  $('shell').onclick = () => launch(selected.path, 'shell').catch(error => notice(error.message));
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
  refresh().catch(showAPIError);
  setInterval(() => refresh().catch(showAPIError), 2000);
})();
