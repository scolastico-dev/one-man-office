// Mobile has one native WebView. Keep opened board/proxy documents in it.
(() => {
  window.open = (url) => {
    const navigate = (target) => {
      const dest = new URL(target, window.location.href);
      if (dest.protocol === 'https:' || dest.protocol === 'http:') window.location.assign(dest.href);
    };
    const location = { set href(value) { navigate(value); }, replace: navigate, assign: navigate };
    const reservation = { location, closed: false, close() { this.closed = true; }, focus() {} };
    if (url && url !== 'about:blank') navigate(url);
    return reservation;
  };
  document.addEventListener('click', (event) => {
    const link = event.target.closest?.('a[target="_blank"]');
    if (link) { event.preventDefault(); window.open(link.href); }
  });
})();
