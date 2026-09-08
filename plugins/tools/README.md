# Tools plugin

The bundled `tools` plugin provides user-triggered maintenance presets. Each
manual action sends a carefully scoped request to the CEO through `omo send`;
the plugin environment uses the reserved system identity automatically.

Use `omo plugin actions tools` to list the presets, then run one with
`omo plugin trigger tools <action>`. The CEO is asked to wait for current work,
queue and delegate the review, inspect before deletion, preserve user work,
and avoid destructive shortcuts.

The `freeze-office` action is the connectivity-safe exception. One Lua action
freezes every new agent spawn and broadcasts instructions for non-CEO agents to
halt and park with `omo wait`. The CEO remains at its prompt. After connectivity
returns, tell the CEO to unfreeze the office; it broadcasts the global wake-up
mail and resumes spawning atomically with the CEO-only `omo office unfreeze`.

Ordinary setup and startup install this plugin only when it is missing and
preserve local edits. `omo setup --update` deliberately replaces it with the
version bundled into the current `omo` binary.
