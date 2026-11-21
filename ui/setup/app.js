const invoke = window.__TAURI__.core.invoke;
const form = document.querySelector('#connection');
const field = (name) => form.elements.namedItem(name);
const element = (id) => document.getElementById(id);
let current;
let busy = false;
function modes() {
  const local = field('mode').value === 'local';
  const relay = field('mode').value === 'relay';
  element('direct').hidden = local || relay;
  element('direct').disabled = local || relay;
  element('relay').hidden = !relay;
  element('relay').disabled = !relay;
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
    for (const name of ['mode','origin','auth','username','notification_mode']) field(name).value = state.config[name];
    field('relay_origin').value = state.config.mode === 'relay' ? state.config.origin : '';
    field('host_id').value = state.config.host_id || '';
    field('relay_allow_loopback_http').checked = !!state.config.relay_allow_loopback_http;
    for (const name of ['remember','notifications','stop_with_window']) field(name).checked = state.config[name];
  }
  const localOption = field('mode').querySelector('option[value=local]');
  if (!state.android && localOption) { localOption.hidden = false; localOption.disabled = false; }
  if (state.android) {
    field('mode').querySelector('option[value=local]')?.remove();
    element('mode-label').hidden = false;
    if (field('mode').value === 'local') field('mode').value = 'direct';
    element('stop-with-window-label').hidden = true;
    element('close-hint').hidden = true;
    field('stop_with_window').checked = false;
  }
  const relay = state.relay;
  const info = relay?.phase === 'awaiting_accept' ? relay.pairing : null;
  element('pairing').hidden = !info;
  element('pair-pin').textContent = info?.pin || '';
  element('pair-command').textContent = info ? `omo relay pair accept -- ${info.aid} ${info.pin}` : '';
  element('pair-public-key').textContent = info?.public_key || '';
  element('pair-key-command').textContent = info ? `omo relay pair accept --public-key ${info.public_key} -- ${info.aid}` : '';
  element('relay-status').textContent = {pairing:'Proving the host and this device…',awaiting_accept:'Accept on the host. The board opens automatically.',connecting:'Reconnecting with saved trust…',ready:'Relay connected.',reconnecting:'Connection lost. Reconnecting with saved trust…',unavailable:'Host or relay unavailable. Retrying saved trust only.',failed:'Pairing failed or expired. Reconnect saved trust, or explicitly start a new pairing.',cancelled:'Cancelled. Reconnect saved trust, or explicitly start a new pairing.'}[relay?.phase] || '';
  element('disconnect').hidden = !state.connected && !['pairing','awaiting_accept','connecting','reconnecting','unavailable'].includes(relay?.phase);
  element('notification-mode-label').hidden = !state.android;
  element('saved-hint').hidden = !(state.connected || state.config?.remember);
  element('recovery').hidden = !state.recovery;
  element('status').textContent = state.connected ? (state.error ? 'Connected. Notification status needs attention.' : state.polling ? 'Connected. System notifications are active.' : 'Connected. Native notification polling is off or unavailable on this server.') : 'Choose your board to connect.';
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
    if (field('mode').value === 'relay') {
      await relayAction(false);
      return;
    }
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

async function relayAction(newPair) {
  const settings = {origin:field('relay_origin').value,host_id:field('host_id').value,admission:field('admission').value || null,new_pair:newPair,relay_allow_loopback_http:field('relay_allow_loopback_http').checked,notifications:field('notifications').checked,notification_mode:field('notification_mode').value};
  try { render(await invoke('relay_connect',{settings}),true); } finally { field('admission').value=''; }
}
element('new-pair').addEventListener('click',()=>void action(()=>relayAction(true)));
window.__TAURI__.event?.listen('relay-state', event=>render(event.payload));
