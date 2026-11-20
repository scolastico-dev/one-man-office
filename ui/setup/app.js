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
  current = state;
  if (load && state.config) {
    for (const name of ['mode','origin','auth','username']) field(name).value = state.config[name];
    for (const name of ['remember','notifications']) field(name).checked = state.config[name];
  }
  const localOption = field('mode').querySelector('option[value=local]');
  if (!state.android && localOption) { localOption.hidden = false; localOption.disabled = false; }
  if (state.android) {
    field('mode').querySelector('option[value=local]')?.remove();
    element('mode-label').hidden = true;
    field('mode').value = 'direct';
    element('notifications-label').hidden = true;
    field('notifications').checked = false;
  }
  element('saved-hint').hidden = !(state.connected || state.config?.remember);
  element('disconnect').hidden = !state.connected;
  element('recovery').hidden = !state.recovery;
  element('status').textContent = state.connected ? (state.polling ? 'Connected. System notifications are active.' : 'Connected. Native notification polling is off or unavailable on this server.') : 'Choose your board to connect.';
  element('problem').textContent = state.error;
  element('problem').hidden = !state.error;
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
    if (field('mode').value === 'local') { await invoke('local_connect'); return; }
    const settings = Object.fromEntries(['mode','origin','auth','username','secret'].map(name => [name, field(name).value]));
    for (const name of ['notifications','remember','reset_identity']) settings[name] = field(name).checked;
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
