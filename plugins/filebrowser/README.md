# Filebrowser company plugin

`filebrowser` is the bundled global company plugin example. It adds a Files
toolbar action to the company dashboard, a Browse button for project setup, and
guarded Unix file transfers in an overlay. It does not add a sidebar panel.

## Manifest

The manifest declares the normal plugin metadata and a `company_load` hook:

```json
{
  "name": "filebrowser",
  "version": "0.1.0",
  "description": "Portable file browser and project directory picker",
  "default_config": {
    "download_warn_bytes": 52428800,
    "download_max_bytes": 1073741824,
    "upload_warn_bytes": 52428800,
    "upload_max_bytes": 1073741824
  },
  "hooks": [
    {"event": "company_load", "javascript": "web/main.js", "files": ["web/helpers.js", "web/style.css"]}
  ]
}
```

`name`, `version`, `description`, `default_config`, and `hooks` follow the
plugin manifest rules in [Writing plugins](../../wiki/plugins.md). A
`company_load` hook must declare `javascript`; it may declare regular asset
files relative to the plugin directory. The JavaScript entrypoint is exposed
automatically at `/plugins/filebrowser/web/main.js`; the declared helper and
stylesheet files are exposed at their matching namespaced URLs.

The script registers with the company page using the named load event:

```javascript
window.omo.onLoad('filebrowser', event => {
  // event.detail.config is the frozen, resolved filebrowser configuration.
});
```

`event.detail.config` is copied into plugin-owned state and is never mutated.
The exact four manifest defaults are used whenever a value is absent or
invalid. A global `plugins.installed.filebrowser.config` entry in the global
`config.yaml` overrides those defaults.

## Company browser API

The page exposes a small frozen `window.omo` object:

- `execute(command, args, options)` runs literal argv without a shell. The
  optional `options.stdin` accepts a string, `Uint8Array`, `Blob`, or `File`.
  With stdin, the browser sends a multipart request containing a JSON
  `request` part followed by the `stdin` part; without stdin it sends the
  regular JSON request. `options.onOutput` receives NDJSON stdout/stderr
  events, and `options.signal` can cancel the command. Output and errors must
  not be used to persist file contents or capabilities.
- `onLoad(pluginName, listener)` listens for the matching
  `omo:company_load` event and returns an unsubscribe function.
- `ids` contains stable dashboard IDs for `sidebar`, `main`, `toolbar`,
  `status`, and `terminals`.
- `$` looks up a DOM element by ID.
- `token` is the in-memory capability from the access URL. Never render, log,
  persist, or send it elsewhere. It is empty in Basic-auth and unsafe modes.

Declared files are same-origin assets, not a secret store. Company plugins are
trusted code and run with the user's authority.

## UI and behavior

The plugin uses square dashboard styling and plugin-owned minimal `<dialog>`
alert, confirm, and prompt helpers. It uses the dashboard theme variables such
as `--surface`, `--border`, `--muted`, `--accent`, and `--danger`; its reduced
motion rule disables transitions under `prefers-reduced-motion: reduce`.

On supported Unix hosts, the Files toolbar action opens an overlay that lists
the whole disk within the process permissions, shows directories and regular
entries, supports hidden files, sorting, breadcrumbs, refresh, and a new-folder
prompt. The project-dialog Browse button opens the same overlay in directory
picker mode, selecting a normalized absolute directory and emitting `input` and
`change` events for project creation.

Downloads first verify that the source is a regular file and obtain its byte
size with a portable argv-only command. The plugin refuses files above
`download_max_bytes`, warns above `download_warn_bytes`, and explains that
large files should be fetched directly with tools such as `ssh` or `scp`.
Base64 stdout is decoded incrementally across arbitrary NDJSON and line
boundaries, then downloaded through a temporary object URL using the basename.

Uploads accept multiple files into the current directory. Each filename must
be one safe path component. Files above `upload_max_bytes` are refused and
files above `upload_warn_bytes` receive the same direct-transfer warning. An
existing destination gets its own overwrite confirmation; declining it skips
that file and continues with later selections. Writes use the generic
`execute` stdin option and portable `dd of=<absolute-path>` argv. The list is
refreshed after every successful write.

The four limits default to 50 MiB warning and 1 GiB maximum in both
directions. Downloads show determinate decoded-byte progress when larger than
a few MiB; uploads show an indeterminate progress state for larger files and
always show the current filename and transfer index. Progress is cleared on
success, failure, and cancellation. File contents remain in page memory only.

The plugin performs one platform probe. On Windows or a failed probe it keeps
Files in the dashboard toolbar, sets its title to exactly `The file manager is
not supported on Windows`, shows the same exact warning inside the overlay, and
disables Files navigation, picker Browse, upload, download, new-folder, and
refresh actions.

## Bundled global installation

`filebrowser` is owned by the global scope and is installed below
`OMO_HOME/plugins/filebrowser` with `builtin:filebrowser` in the independent
global `config.yaml`. It is available to every company dashboard and is not
copied into office-local `.omo/plugins` directories. The global plugin update
setting controls managed refreshes; local edits are preserved unless the
explicit bundled update flow owns that installation.

Disable it with `omo plugin disable --global filebrowser`. The configuration entry and
directory remain, so it is not loaded. Removing the configuration entry while
retaining the directory opts out of automatic bundled reclaim; the directory
is treated as user-owned until explicitly configured again.
