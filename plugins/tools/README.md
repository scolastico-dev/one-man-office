# Tools plugin

The bundled `tools` plugin provides user-triggered maintenance presets. Its
maintenance actions send carefully scoped requests to the CEO through
`omo send`; the plugin environment uses the reserved system identity
automatically.

Use `omo plugin actions tools` to list the presets, then run one with
`omo plugin trigger tools <action>`. The CEO is asked to wait for current work,
queue and delegate the review, inspect before deletion, preserve user work,
and avoid destructive shortcuts.

The `freeze-office` action is a cooperative, plugin-only connectivity freeze.
Its Lua hook broadcasts urgent role-specific instructions: the CEO runs the
existing `omo office halt-spawns` command and remains at its prompt, while all
other agents halt and park with `omo wait`. After connectivity returns, the user
tells the CEO to send a global urgent wake-up mail and then run the existing
`omo office resume-spawns` command.

Ordinary setup and startup install this plugin only when it is missing and
preserve local edits. `omo setup --update` deliberately replaces it with the
version bundled into the current `omo` binary.
