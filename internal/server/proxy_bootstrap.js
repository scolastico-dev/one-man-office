// This script runs only on omo's sign-in page, never in an application.
(() => {
  const grant = location.hash.slice(1)
  history.replaceState(null, '', location.pathname)
  const status = document.getElementById('status')
  if (!grant) {
    status.textContent = 'Open this Web UI again from the board.'
    return
  }
  fetch('/_omo/proxy/exchange', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    referrerPolicy: 'no-referrer',
    body: JSON.stringify({ grant }),
  }).then(async (response) => {
    if (!response.ok) throw new Error('exchange failed')
    const { url } = await response.json()
    location.replace(url)
  }).catch(() => {
    status.textContent = 'This Web UI sign-in expired or is unavailable. Open it again from the board.'
  })
})()
