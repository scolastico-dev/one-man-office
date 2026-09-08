# Tools plugin

The bundled `tools` plugin provides user-triggered maintenance presets. Each
manual action sends a carefully scoped request to the CEO through `omo send`;
the plugin environment uses the reserved system identity automatically.

Use `omo plugin actions tools` to list the presets, then run one with
`omo plugin trigger tools <action>`. The CEO is asked to wait for current work,
queue and delegate the review, inspect before deletion, preserve user work,
and avoid destructive shortcuts.

The `freeze-office` action is the connectivity-safe exception: it uses the
Lua plugin runtime to invoke `omo office freeze`, which durably halts all
spawning, tells every agent to stop, and parks non-CEO agents. Run
`omo office unfreeze` after the connection returns to send global wake-up mail
and resume spawning.

Ordinary setup and startup install this plugin only when it is missing and
preserve local edits. `omo setup --update` deliberately replaces it with the
version bundled into the current `omo` binary.
