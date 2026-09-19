# Bugreport plugin

`bugreport` is an optional Git-installed plugin for reporting failures in omo
itself, such as wrong routing, stuck lifecycle state, bad prompts, crashes, or
CLI errors. It is not a project-bug tracker, is not bundled, and is never
copied into an office automatically.

The official plugin is version `1.0.0` from the `release` branch. Install it
for one office with:

```bash
omo plugin install \
  --name bugreport \
  --subpath plugins/bugreport \
  --branch release \
  https://github.com/scolastico-dev/one-man-office.git
```

Install it for every office under the current omo home with:

```bash
omo plugin install --global \
  --name bugreport \
  --subpath plugins/bugreport \
  --branch release \
  https://github.com/scolastico-dev/one-man-office.git
```

## Configuration

Configure `plugins.installed.bugreport.config` in the office configuration (or
in the global configuration for a global installation):

| Key | Default | Meaning |
| --- | --- | --- |
| `mode` | `github` | `github` creates an issue; `local` writes a Markdown report. |
| `repository` | `scolastico-dev/one-man-office` | GitHub repository passed to `gh issue create -R`. |
| `labels` | `["bug", "omo-report"]` | Labels passed to GitHub issue creation. |
| `local_dir` | `""` | Report directory. Empty resolves to `<OMO_HOME>/bugreports`, or the platform default omo home followed by `bugreports`. |
| `fallback_local` | `true` | In GitHub mode, write locally when authentication or issue creation fails. |
| `review_before_publish` | `false` | Agents other than the user and CEO write locally and do not publish to GitHub. |
| `instruct` | `true` | Adds short bugreport guidance to prompts for report-capable roles. |

An empty `local_dir` uses `OMO_HOME` when set. Otherwise Unix uses
`~/.local/omo/bugreports`, and Windows uses `%APPDATA%\\omo\\bugreports`.
Directories are created on demand. Reports use a safe
`YYYYMMDD-HHMMSS-<slug>.md` filename; collisions receive a suffix and never
overwrite an existing report.

## Report action

The exact syntax is:

```text
omo plugin trigger bugreport report -- body=<absolute-path> "<title>"
```

There must be exactly one non-empty title and one `body=` path. The path must
be absolute, readable UTF-8, non-empty after trimming, and no larger than
61,440 bytes. It must contain these headings, case-insensitively:

```text
## Summary
## Observed behavior
## Expected behavior
## Steps or evidence
## Anonymization check
```

The plugin appends an `## Environment` section containing only the omo
version, OS/architecture, effective mode, authoritative caller role, and
plugin name. Do not put project names, paths, repository or branch names,
customer data, secrets, or mail contents in the report. The body is otherwise
authored by the caller.

In `local` mode the result is:

```text
file: <path>
```

In `github` mode the plugin runs `gh auth status`, then creates an issue with
the configured title, finished body, repository, and labels. A successful
result is:

```text
issue: <url> (created)
```

GitHub authentication or issue-creation failures use local fallback when
`fallback_local` is enabled. The result remains the exact one-line `file:`
contract; the fallback is identified in the notification subject and plugin
log. With fallback disabled, the action returns a clear failure and writes no
report. When `review_before_publish` is enabled, the user and CEO may publish;
all other report roles write locally and do not invoke `gh`.

The exact result line is mailed to both the user and CEO through omo's system
sender and is written to the plugin log. Usage and body-validation failures
include the exact invocation and all required headings; the same guidance is
best-effort mailed to a non-user caller.

The `report` action permits `user`, `ceo`, `product_manager`, `developer`,
`reviewer`, `freelancer`, and `firefighter`.

## Notice action

The user can send a free-text notice to the living CEO:

```text
omo plugin trigger bugreport notice -- "<message>"
```

The action uses only public CLI behavior: it runs `omo agent list`, finds a
living CEO, and sends one literal, anonymized-investigation block to that CEO
with `omo type ... --key enter`. It preserves multiline text and shell
metacharacters as literal input. Blank messages, messages whose complete block
reaches the 64 KiB agent-input limit, and offices without a living CEO fail
without typing anything. Only the `user` role may invoke `notice`.

## Prompt guidance

When `instruct` is enabled, the plugin adds an idempotent short note to
`user`, `ceo`, `product_manager`, `developer`, `reviewer`, `freelancer`, and
`firefighter` prompts. It explains that bugreport is for omo failures rather
than project bugs, requires anonymization and the five headings above, and
gives the exact `report` trigger syntax. Disabled guidance adds nothing.
