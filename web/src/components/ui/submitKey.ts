// The key combination that sends a comment or saves a description, named for
// the platform. Both Ctrl+Enter and Cmd+Enter work everywhere (the handlers
// accept either key); the hint shows the one the platform's users press.
export function submitKeyName(mac = isMacPlatform()): string {
  return mac ? 'Cmd+Enter' : 'Ctrl+Enter'
}

function isMacPlatform(): boolean {
  return typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)
}
