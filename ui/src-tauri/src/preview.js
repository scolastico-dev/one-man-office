// Preview documents are ordinary applications. The setup wrapper is unnecessary
// here; the native ACL still denies every command even if an app imports an SDK.
Object.defineProperty(window, '__TAURI__', { value: undefined, configurable: false });
