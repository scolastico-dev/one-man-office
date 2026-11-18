import { expect, test } from 'vitest'
import { isPortCollision } from '../../e2e/support/startup'

test('startup retries only a bind collision on the test server TCP port', () => {
  expect(isPortCollision('Error: listen on 127.0.0.1:32123: listen tcp 127.0.0.1:32123: bind: address already in use', 32123)).toBe(true)
  expect(isPortCollision('listen tcp [::1]:32123: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.', 32123)).toBe(true)
  for (const unrelated of [
    'plugin: address already in use\nError: invalid settings',
    'listen unix /tmp/omo.sock: bind: address already in use',
    'listen tcp 127.0.0.1:32124: bind: address already in use',
    'listen tcp 127.0.0.1:32123: bind: permission denied',
  ]) expect(isPortCollision(unrelated, 32123), unrelated).toBe(false)
})
