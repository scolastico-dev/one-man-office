# Official plugins

This page is the inventory and installation guide for the plugins shipped in
the omo repository. The bundled plugins are supplied by omo itself; the
official optional plugins are Git-installed from the stable `release` branch.
All seven listed plugins are version `1.0.0` in the current catalog.
For manifest rules and plugin authoring, see [Writing plugins](plugins.md).

## Bundled plugins

### `nudge`

[`nudge` README](../plugins/nudge/README.md)

`nudge` sends direct terminal reminders for unread mail, stale work, forgotten
`omo done`/`omo wait`, reviewer parking, and retained freelancers. It records
activity in plugin-local storage and never creates mail. It is bundled,
automatic, and office-owned: it is installed below `.omo/plugins/nudge` when
missing, not in the global plugin root.

Setup and management for the current office:

```bash
omo setup                         # install missing bundled office plugins
omo plugin disable nudge          # keep installed, stop loading its hooks
omo plugin enable nudge
omo setup --update                # explicitly replace editable bundled files
```

There is no global `nudge` installation or global management command because
its ownership scope is the office. Normal office startup also installs it when
missing. `omo setup --update` is the bundled replacement path and may replace
local bundled-plugin edits after its preview/approval flow.

Configuration lives at `plugins.installed.nudge.config` in `.omo/omo.yaml`.
The key settings are `check_interval` (default `1m`),
`activity_sample_interval` (default `30s`), and the `after`/`repeat` reminder
durations for `inbox`, `smokealarm_done`, `park_completed`, `reviewer_wait`,
`freelancer_waiting`, `no_job_wait`, and `stale_work`. Restart the office after
changing configuration. The manifest has no manual actions.

### `tools`

[`tools` README](../plugins/tools/README.md)

`tools` provides user-triggered maintenance presets that ask the CEO to queue
careful repository or office reviews; `freeze-office` halts new work-agent
spawns and tells agents to park. It is bundled, automatic, and office-owned,
installed below `.omo/plugins/tools` when missing. It is not a global plugin.

Setup and management for the current office:

```bash
omo setup                         # install when the name is not already owned
omo plugin disable tools
omo plugin enable tools
omo setup --update                # explicitly replace editable bundled files
omo plugin actions tools          # list the available manual actions
```

There is no global `tools` installation or global management command. Normal
startup preserves existing local edits; `omo setup --update` is the explicit
replacement path for the omo-owned bundled copy.

Every manifest manual action accepts no arguments and is restricted to the
`user` role (the manifest's default):

| Action | Purpose and syntax |
| --- | --- |
| `freeze-office` | Halt spawning and broadcast freeze instructions. `omo plugin trigger tools freeze-office` |
| `repository-cleanup` | Queue a comprehensive repository cleanup after current work. `omo plugin trigger tools repository-cleanup` |
| `storage-cleanup` | Queue a safe audit of obsolete `.omo/storage` artifacts and messages. `omo plugin trigger tools storage-cleanup` |
| `security-audit` | Queue a repository security audit after current work. `omo plugin trigger tools security-audit` |
| `dependency-audit` | Queue a dependency and update audit after current work. `omo plugin trigger tools dependency-audit` |
| `quality-audit` | Queue a test-gap and code-quality audit after current work. `omo plugin trigger tools quality-audit` |

The manifest has no plugin configuration keys.

### `filebrowser`

[`filebrowser` README](../plugins/filebrowser/README.md)

`filebrowser` adds the Files toolbar action and project-setup directory picker
to the company dashboard, with guarded browsing, uploads, and downloads on
POSIX and Windows. It is bundled, automatic, and global-owned: omo installs it
below `OMO_HOME/plugins/filebrowser` and makes it available to every company
dashboard. It is never copied into an office's `.omo/plugins` directory.

There is no Git installation command. Opening the global omo home (including
`omo` or `omo company`) ensures the bundled global installation when it is
missing. Manage the configured global installation with:

```bash
omo plugin disable --global filebrowser
omo plugin enable --global filebrowser
omo plugin update --global filebrowser
```

Disabling keeps the configuration entry and directory. Removing the global
configuration entry while retaining the directory opts out of automatic
bundled reclaim. The global plugin update setting controls ordinary refreshes;
omo-owned copies retain their bundled marker.

Configuration is in `plugins.installed.filebrowser.config` in the global
`config.yaml`:

| Key | Default | Meaning |
| --- | ---: | --- |
| `download_warn_bytes` | `52428800` | Confirm downloads above 50 MiB. |
| `upload_warn_bytes` | `52428800` | Confirm uploads above 50 MiB. |
| `upload_max_bytes` | `1073741824` | Refuse uploads above 1 GiB. |

The only manifest manual action is `download`, which accepts exactly one
absolute regular-file path and is restricted to the `user` role:

```text
omo plugin trigger --global filebrowser download -- <absolute-file-path>
```

The action creates an authenticated company download link. The company UI
normally invokes the same action through its dashboard API.

## Official optional plugins

These plugins are non-embedded and are never installed automatically. Each
can be installed for one office or in the global omo home. After changing
configuration, restart the office (or company dashboard for a global-only
company hook) as applicable.

### `pushover`

[`pushover` README](../plugins/pushover/README.md)

`pushover` sends a notification when unread office mail remains unchanged for
the configured stability window, and provides an immediate alert action. It is
optional and Git-installed; choose office scope for one office or global scope
for the shared global plugin root.

Install and manage one office:

```bash
omo plugin install https://github.com/scolastico-dev/one-man-office.git --name pushover --subpath plugins/pushover --branch release
omo plugin update pushover
omo plugin enable pushover
omo plugin disable pushover
```

Install and manage globally:

```bash
omo plugin install https://github.com/scolastico-dev/one-man-office.git --name pushover --subpath plugins/pushover --branch release --global
omo plugin update --global pushover
omo plugin enable --global pushover
omo plugin disable --global pushover
```

Required configuration is `user_key` and `app_token`. Other defaults are
`api_url`, `check_interval`, `stable_window`, `priority`, and `sound` under
`plugins.installed.pushover.config` in the selected scope's configuration.

The manifest's only manual action is `notify`: it sends an immediate
notification, requires one message and accepts one optional title, and allows
roles `user` and `ceo`:

```text
omo plugin trigger pushover notify -- "<message>" ["<optional-title>"]
```

### `autoshutdown`

[`autoshutdown` README](../plugins/autoshutdown/README.md)

`autoshutdown` requests an orderly `omo safe-shutdown` after the office has
been quiet for the configured period. It is optional and Git-installed; it is
not embedded or automatic.

Install and manage one office:

```bash
omo plugin install https://github.com/scolastico-dev/one-man-office.git --name autoshutdown --subpath plugins/autoshutdown --branch release
omo plugin update autoshutdown
omo plugin enable autoshutdown
omo plugin disable autoshutdown
```

Install and manage globally:

```bash
omo plugin install https://github.com/scolastico-dev/one-man-office.git --name autoshutdown --subpath plugins/autoshutdown --branch release --global
omo plugin update --global autoshutdown
omo plugin enable --global autoshutdown
omo plugin disable --global autoshutdown
```

Configuration is `idle_after` (default `30m`), `exempt_roles` (default
`[ceo, smokealarm]`), and `check_interval` (default `30s`) under
`plugins.installed.autoshutdown.config` in the selected scope. The manifest
has no manual actions; the plugin's cron hook is automatic once enabled.

### `pullrequest`

[`pullrequest` README](../plugins/pullrequest/README.md)

`pullrequest` handles `asis` jobs: it pushes the job branch and creates or
updates an idempotent pull request or merge request on GitHub, Forgejo/Gitea,
or GitLab. It is optional and Git-installed; it is not embedded or automatic.

Install and manage one office:

```bash
omo plugin install https://github.com/scolastico-dev/one-man-office.git --name pullrequest --subpath plugins/pullrequest --branch release
omo plugin update pullrequest
omo plugin enable pullrequest
omo plugin disable pullrequest
```

Install and manage globally:

```bash
omo plugin install https://github.com/scolastico-dev/one-man-office.git --name pullrequest --subpath plugins/pullrequest --branch release --global
omo plugin update --global pullrequest
omo plugin enable --global pullrequest
omo plugin disable --global pullrequest
```

The flat configuration under `plugins.installed.pullrequest.config` includes
`remote` (`origin`), `forge` (`auto`), `api_url`, `gitlab_hosts`, `token`,
`token_env`, and `instruct` (true). The agreed multi-server interface also
supports an ordered `servers` array: entries provide per-host GitHub,
Forgejo/Gitea, or GitLab credentials and remotes from one installation. The
first normalized-host match wins; an unmatched host keeps the flat-config
behavior and falls back to those existing keys.

The manifest's only manual action is `create`. It pushes the current job
branch and creates or updates the request; it accepts a body file and optional
repository selector/title, and allows roles `user`, `ceo`, `product_manager`,
`developer`, and `freelancer`:

```text
omo plugin trigger pullrequest create -- [repo=<key>] body=<absolute-path> "<title>"
```

`body=` is required and must name an absolute, non-empty UTF-8 Markdown file
containing `## Summary`, `## What changed`, `## Why`, and `## How it was
verified`. Repeating the action updates an existing open request safely.

### `bugreport`

[`bugreport` README](../plugins/bugreport/README.md)

`bugreport` is version `1.0.0`. It reports omo's own behavior, not project
bugs. It is optional, non-bundled, and installed from the `release` branch.
Reports must exclude
project names, paths, repositories, branches, customer data, secrets, and mail
contents.

Install and manage one office:

```bash
omo plugin install --name bugreport --subpath plugins/bugreport --branch release https://github.com/scolastico-dev/one-man-office.git
omo plugin update bugreport
omo plugin enable bugreport
omo plugin disable bugreport
```

Install and manage globally:

```bash
omo plugin install --global --name bugreport --subpath plugins/bugreport --branch release https://github.com/scolastico-dev/one-man-office.git
omo plugin update --global bugreport
omo plugin enable --global bugreport
omo plugin disable --global bugreport
```

The default configuration is `mode: github`, repository
`scolastico-dev/one-man-office`, labels `bug` and `omo-report`, empty
`local_dir` (the global omo home's `bugreports` directory), `fallback_local:
true`, `review_before_publish: false`, and `instruct: true`. See the
[`bugreport` README](../plugins/bugreport/README.md) for the complete
configuration and fallback behavior.

Its manifest manual actions are:

| Action | Purpose, arguments, and roles |
| --- | --- |
| `report` | Publish a full omo report to GitHub or the local fallback. Requires roles `user`, `ceo`, `product_manager`, `developer`, `reviewer`, or `freelancer`; syntax: `omo plugin trigger bugreport report -- body=<absolute-path> "<title>"`. The body must contain `## Summary`, `## Observed behavior`, `## Expected behavior`, `## Steps or evidence`, and `## Anonymization check`. Privacy-safe environment metadata is appended and the result is `issue: <url> (created)` or `file: <path>`. |
| `notice` | Send a free-text observation to the living CEO so omo behavior can be investigated and turned into an authored, anonymized report. User-only; syntax: `omo plugin trigger bugreport notice -- "<observation>"`. |

GitHub is the default publishing path. With `fallback_local: true`, an
unavailable GitHub publish is written to the configured local report directory;
`review_before_publish: true` requires review before a GitHub publish. The
plugin's privacy checks and exact local/GitHub behavior are documented in the
[`bugreport` README](../plugins/bugreport/README.md).
