(config) => {
  const { origin, pathname } = window.location;
  if (origin !== config.origin || pathname.startsWith('/proxy/') || pathname.startsWith('/_omo/')) return;
  if (config.auth === 'token') {
    // Mobile injects after document load: same-document capture also unlocks an already loaded board.
    if (config.capture && window.location.hash !== '#' + config.secret) window.location.hash = config.secret;
    try { window.localStorage.setItem('omo.token', config.secret); } catch {
      // The board's existing fragment capture keeps an in-memory fallback when storage is blocked.
      if (window.location.hash !== '#' + config.secret) {
        window.location.replace(window.location.pathname + window.location.search + '#' + config.secret);
        return;
      }
    }
  } else {
    try { window.localStorage.removeItem('omo.token'); } catch { /* no stored token */ }
  }
  Object.defineProperty(window, '__OMO_NATIVE_UI__', { value: Object.freeze({ notifications: config.native }), configurable: true });
  if (config.native) {
    // Also covers a server board built before it knew the native-ui marker.
    Object.defineProperty(window, 'Notification', { value: undefined, configurable: true });
  }
  const configure = () => {
    if (window !== window.top || document.getElementById('omo-ui-connection')) return;
    const button = document.createElement('button');
    button.id = 'omo-ui-connection';
    button.type = 'button';
    button.textContent = 'Connection…';
    button.setAttribute('aria-label', 'Configure omo ui connection');
    Object.assign(button.style, { position: 'fixed', bottom: '8px', right: '8px', zIndex: '2147483647', font: 'inherit', padding: '6px 10px', borderRadius: '6px', cursor: 'pointer' });
    button.addEventListener('click', () => { window.location.href = 'omo-ui:configure'; });
    document.body.append(button);
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', configure, { once: true });
  else configure();
}
