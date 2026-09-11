# Global home and office trust

`omo setup` and writable office startup initialize a user-wide home at
`~/.local/omo` on Linux/macOS or `%APPDATA%/omo` on Windows. Set `OMO_HOME` to
an absolute path to use a separate home, for example in automated tests.

```text
omo/
  config.yaml                 # independent global settings; never merged into office YAML
  config.lock                 # serializes global configuration writes
  known_plugins.json          # user-maintained recommendation overrides and additions
  known_plugins.example.json  # copyable official catalog reference
  plugins/                    # shared event plugins; filebrowser is installed here
  extensions/                 # shared role prompt additions; initially empty
  template/                   # new-office overlay; initially empty
  superpowers/                # shared Superpowers checkout
  company/                 # private browser lifecycle, startup log, and autostart settings
```

The strict global `config.yaml` starts with:

```yaml
trusted_offices: []
template:
  enabled: false       # apply template/.omo/omo.yaml to every office at load
  auto_sync: false     # also persist that overlay on normal startup
  setup_never_ask: false
plugins:
  update_on_start: true
  installed: {}
```

There are no global `messages` or `prompts` directories. The template is a copy
source for new offices, not a runtime fallback.

## Office trust

Before starting agents or performing startup updates, `omo` resolves the
office's absolute location (following symlinks) and asks whether you trust it.
Accepting adds that canonical location atomically to `trusted_offices`;
declining or EOF aborts startup. Approval applies to that location only, not
to its children.

Headless or piped input cannot silently approve an unknown office: use
`omo --trust-office --no-tui` to explicitly approve the current location and
persist that choice. Neither `--mock` nor `--skip-startup-checks` bypasses
trust. Setup, read-only observation, help/version, management, and agent
commands do not prompt.

Trust grants the office's configuration and plugins permission to run commands
as you.

## Interactive setup form

On a terminal, `omo setup` detects every supported agent CLI and opens a form.
Each role gets profile checkboxes with the current defaults preselected and an
assignment-method selector; a separate checkbox list controls bundled and
catalog plugins. Official catalog entries sort first and display an
`[official]` label with their description. Use `--non-interactive` for the
auto-detected single-provider defaults in CI or scripts.

When the global template is active (`template.enabled` or
`template.auto_sync`) and its role assignments still resolve to available
profiles, setup asks whether to skip those role questions. The prompt defaults
to yes. If every role is defined, one confirmation skips all role and
assignment questions. If only some roles are defined, the confirmation lists
them and setup asks only about the remaining roles. Answering no restores the
full role form. Roles whose template profiles are unavailable are not treated
as defined, and inactive templates do not show this confirmation.

The optional final prompts can save your model/role choices into
`template/.omo/omo.yaml`, install selected catalog plugins globally (and omit
their local copies), or remember not to ask about global setup choices again
(`template.setup_never_ask`).

New global homes receive an empty `known_plugins.json`; setup always merges the
embedded official Pushover and autoshutdown entries into its effective catalog.
`known_plugins.example.json` contains both copyable official objects, each with
`official: true` and a `release` branch pin. An omitted `official` field in a
user-added entry means `false`. Existing homes keep their own
`known_plugins.json`; copy either or both official objects from the example
file when you want to add or override them.
The strict catalog fields are `name`, `description`, `source`, optional
`subpath`, optional `branch`, and optional `official`. OMO developers do not
control entries added to this user-maintained catalog. Plugins get CLI access,
so inspect every source and install only what you trust.

## New-office template

Put files in `template/` at their desired paths relative to a new office root.

- `template/.omo/omo.yaml` is a **partial YAML override** layered onto the
  generated office config. It may not replace `repos`, so detected
  repositories and new built-in settings remain intact.
- `template/.omo/prompts/developer.md` replaces that exported role prompt.
- `template/notes/welcome.md` creates an ordinary office file.

Fresh setup exports the embedded defaults first, then copies every regular file
in the template recursively, replacing matching paths and retaining file
permissions. Symlinks and special files are rejected before any office file is
created. If copying fails later, setup removes its initialization marker so
correcting the filesystem problem and rerunning setup completes the overlay;
partially copied files can remain.

Repeating setup on an existing office and `omo setup --update` do not copy the
template again. `omo setup --sync` reapplies only the partial config override
to an existing office.

When `template.enabled` is true, every office loads its own config first and
then overlays the reserved partial `template/.omo/omo.yaml` at load time.
`template.auto_sync` additionally persists that result on startup.

## Global extensions

Global `extensions/<role>.md` or `extensions/<role>/*.md` follow the same rules
as office extensions. Global content comes first, then office content; each
fragment directory is loaded lexically. Each scope independently requires
either the file or the directory form, never both. See
[Prompts, messages, and extensions](prompts.md#prompt-extensions).

## Global plugins

Global plugins use `plugins/<name>/plugin.json` and the same manifest and
configuration schema as local plugins. Configure managed Git sources under the
global `plugins.installed` mapping; unmanaged directories are also loaded.

```bash
omo plugin --global list
omo plugin --global install https://github.com/acme/omo-plugin.git
omo plugin --global update
omo plugin --global disable <name>
omo plugin trigger --global <name> <action> # no running office required
```

- The global `update_on_start` switch controls startup updates independently
  of the office switch. `--skip-startup-checks` skips both scopes.
- Managed checkouts are cached in global `plugins/.repos`; plugin runtime and
  storage data stays in each office's database.
- A managed plugin is shallow-cloned from its source repository once per cache
  name. Selecting a `subpath` limits the activated files, not the clone: a
  monorepo is still transferred as the whole shallow repository, so large
  repositories can make installation more expensive than the activated plugin
  size suggests.
- A local plugin directory or installed configuration entry shadows the same
  global installation name, including a disabled local entry.
- Selected hooks execute in lexical directory-name order using their own
  scope's config. Duplicate manifest names across different installation names
  fail startup.
- Global managed updates and startup loading share a process-level file lock
  at `plugins/.update.lock`. Each running office uses its own snapshot of the
  selected global plugin files, so updates affect subsequent launches without
  changing an existing office's code or resources. Snapshots live in the system
  temporary directory and are removed on orderly office close.

See [Writing plugins](plugins.md) for the manifest and runtime API.
