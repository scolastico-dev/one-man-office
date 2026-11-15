// Applies the remembered theme before the first paint. Loaded as a classic,
// blocking script in <head> (the Content-Security-Policy forbids inline
// scripts). Keep in step with readTheme and applyTheme in theme.ts.
;(function () {
  try {
    var choice = window.localStorage.getItem('omo.theme')
    if (choice === 'dark' || choice === 'light') {
      document.documentElement.setAttribute('data-theme', choice)
    }
  } catch (e) {
    // Storage unavailable: follow the system setting.
  }
})()
