'use strict';
// Serialized into the private renderer overlay; no Node APIs in this function.
function createSecretProvider(capability, scope = globalThis) {
  async function call(action, key, value) {
    const response = await scope.fetch('./_omo/secrets', {
      method: 'POST', credentials: 'same-origin', cache: 'no-store', redirect: 'error',
      headers: {'Content-Type': 'application/json', 'X-Editor-Secret': capability},
      body: JSON.stringify({action, key, value}),
    });
    if (!response.ok) throw new Error('Editor secret storage unavailable');
    return (await response.json()).value;
  }
  return {type: 'persisted', get: key => call('get', key), set: (key, value) => call('set', key, value),
    delete: key => call('delete', key), keys: () => call('keys')};
}
if (typeof module !== 'undefined') module.exports = {createSecretProvider};
