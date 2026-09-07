# Tools plugin

The bundled `tools` plugin provides user-triggered maintenance presets. Each
manual action sends a carefully scoped request to the CEO through `omo send`;
the plugin environment uses the reserved system identity automatically.

Use `omo plugin actions tools` to list the presets, then run one with
`omo plugin trigger tools <action>`. The CEO is asked to wait for current work,
queue and delegate the review, inspect before deletion, preserve user work,
and avoid destructive shortcuts.

Ordinary setup and startup install this plugin only when it is missing and
preserve local edits. `omo setup --update` deliberately replaces it with the
version bundled into the current `omo` binary.
