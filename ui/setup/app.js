const invoke = window.__TAURI__.core.invoke;
const form = document.querySelector('#connection');
const field = (name) => form.elements.namedItem(name);
const element = (id) => document.getElementById(id);
let current;
let busy = false;
function modes() {
  const local = field('mode').value === 'local';
  element('direct').hidden = local;
  element('direct').disabled = local;
  element('local-hint').hidden = !local;
  const auth = field('auth').value;
  element('username-label').hidden = auth !== 'basic';
  element('secret-label').hidden = auth === 'none';
  element('remember-label').hidden = auth === 'none';
  element('secret-title').textContent = auth === 'basic' ? 'Password' : 'Login token';
}
function render(state, load = false) {
  if (!state.connected || current?.forward_generation !== state.forward_generation) {
    element('forward-app').replaceChildren();
    showExposures([]);
    for (const id of ['preview-forward','expose-forward']) element(id).disabled = true;
    if (state.connected) element('forward-exposure-status').textContent = 'Refresh apps and status to see active previews and exposures.';
  }
  current = state;
  if (load && state.config) {
    for (const name of ['mode','origin','auth','username','notification_mode']) field(name).value = state.config[name];
    for (const name of ['remember','notifications','stop_with_window']) field(name).checked = state.config[name];
  }
  const localOption = field('mode').querySelector('option[value=local]');
  if (!state.android && localOption) { localOption.hidden = false; localOption.disabled = false; }
  if (state.android) {
    field('mode').querySelector('option[value=local]')?.remove();
    element('mode-label').hidden = true;
    field('mode').value = 'direct';
    element('stop-with-window-label').hidden = true;
    element('close-hint').hidden = true;
    field('stop_with_window').checked = false;
  }
  element('notification-mode-label').hidden = !state.android;
  element('saved-hint').hidden = !(state.connected || state.config?.remember);
  element('disconnect').hidden = !state.connected;
  element('recovery').hidden = !state.recovery;
  element('status').textContent = state.connected ? (state.error ? 'Connected. Notification status needs attention.' : state.polling ? 'Connected. System notifications are active.' : 'Connected. Native notification polling is off or unavailable on this server.') : 'Choose your board to connect.';
  element('problem').textContent = state.error;
  element('problem').hidden = !state.error;
  element('forwards').hidden = state.android || !state.connected || !state.forward_tunnel;
  modes();
}
async function action(fn) {
  if (busy) return;
  busy = true;
  for (const button of form.querySelectorAll('button')) button.disabled = true;
  element('problem').hidden = true;
  try { await fn(); } catch (error) {
    element('problem').textContent = typeof error === 'string' ? error : 'The connection could not be updated.';
    element('problem').hidden = false;
  } finally {
    busy = false;
    for (const button of form.querySelectorAll('button')) button.disabled = false;
  }
}
form.addEventListener('change', modes);
form.addEventListener('submit', (event) => {
  event.preventDefault();
  void action(async () => {
    if (field('mode').value === 'local') {
      const settings = Object.fromEntries(['notifications','remember','stop_with_window'].map(name => [name, field(name).checked]));
      render(await invoke('local_connect', { settings }), true);
      return;
    }
    const settings = Object.fromEntries(['mode','origin','auth','username','secret','notification_mode'].map(name => [name, field(name).value]));
    for (const name of ['notifications','remember','reset_identity','stop_with_window']) settings[name] = field(name).checked;
    const state = await invoke('connect', { settings });
    field('secret').value = '';
    field('reset_identity').checked = false;
    render(state, true);
  });
});
element('disconnect').addEventListener('click', () => void action(async () => render(await invoke('disconnect'))));
for (const [id,replay] of [['replay',true],['baseline',false]]) element(id).addEventListener('click', () => void action(async () => render(await invoke('recover',{replay}))));
window.addEventListener('focus', () => { if (!busy) void invoke('get_setup').then(state => render(state)).catch(() => {}); });
void action(async () => render(await invoke('get_setup'), true));

let forwardBusy = false;
function showExposures(exposures) {
  element('forward-exposures').replaceChildren();
  element('forward-exposure-status').textContent = exposures.length ? `${exposures.length} active` : 'No previews or local exposures are active.';
  for (const exposure of exposures) {
    const item = document.createElement('li');
    const text = document.createElement('p');
    text.textContent = `${exposure.name}: ${exposure.address} — ${exposure.error || (exposure.preview ? 'Preview available' : 'Listening')}`;
    const stop = document.createElement('button');
    stop.type = 'button'; stop.textContent = exposure.preview ? 'Close preview' : 'End exposure';
    stop.addEventListener('click', () => void forwardAction(async () => showExposures(await invoke('stop_forward', {id:exposure.id}))));
    item.append(text, stop); element('forward-exposures').append(item);
  }
}
async function forwardAction(fn) {
  if (forwardBusy || element('forwards').hidden) return;
  forwardBusy = true;
  element('forward-problem').hidden = true;
  for (const button of element('forwards').querySelectorAll('button')) button.disabled = true;
  try { await fn(); } catch (error) {
    element('forward-problem').textContent = typeof error === 'string' ? error : 'Cannot update app forwarding.';
    element('forward-problem').hidden = false;
  } finally {
    forwardBusy = false;
    for (const button of element('forwards').querySelectorAll('button')) button.disabled = false;
    for (const id of ['preview-forward','expose-forward']) element(id).disabled = !element('forward-app').value;
  }
}
async function refreshForwards() {
  const view = await invoke('get_forwards');
  const select = element('forward-app');
  const selected = select.value;
  select.replaceChildren();
  for (const app of view.forwards) {
    const option = document.createElement('option'); option.value = app.id;
    option.textContent = `${app.name || 'App'}${app.ticket ? ` · Ticket ${app.ticket}` : ''} · ${app.id.slice(0,8)}`;
    select.append(option);
  }
  if (view.forwards.some(app => app.id === selected)) select.value = selected;
  element('forward-status').textContent = view.forwards.length ? 'Apps open at their root address.' : 'No apps are registered. Refresh after starting an app.';
  showExposures(view.exposures);
  if (view.error) {
    element('forward-problem').textContent=view.error;
    element('forward-problem').hidden=false;
  }
}
element('refresh-forwards').addEventListener('click', () => void forwardAction(refreshForwards));
element('preview-forward').addEventListener('click', () => void forwardAction(async () => showExposures(await invoke('preview_forward', {id:element('forward-app').value}))));
element('expose-forward').addEventListener('click', () => void forwardAction(async () => showExposures(await invoke('expose_forward', {id:element('forward-app').value,port:element('forward-port').value}))));

window.addEventListener('omo-forwards-changed', () => void forwardAction(refreshForwards));
