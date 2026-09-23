# Bugreport plugin

`bugreport` is an optional Git-installed plugin for reporting failures in omo
itself, not project bugs. It is version `1.0.0`, is not bundled, and is never
copied into an office automatically.

Install it for one office with:

```bash
omo plugin install --name bugreport --subpath plugins/bugreport --branch release https://github.com/scolastico-dev/one-man-office.git
```

Install it globally with `omo plugin install --global` and the same options.

## Configuration

Configure `plugins.installed.bugreport.config` in the office or global
configuration:

| Key | Default | Meaning |
| --- | --- | --- |
| `mode` | `local` | `local` writes Markdown reports; `github` enables publishing. |
| `auto_publish` | `false` | Only in `github` mode: `true` publishes `report` directly; `false` requires `publish` after consent. |
| `repository` | `scolastico-dev/one-man-office` | GitHub repository used for duplicate search and issue creation. |
| `labels` | `[`"`bug`"`, `"`omo-report`"`]` | Labels passed to GitHub issue creation. |
| `local_dir` | `""` | Report directory; empty resolves to `<OMO_HOME>/bugreports` or the platform default omo home followed by `bugreports`. |
| `fallback_local` | `true` | Applies only when an automatic GitHub publish attempt fails. |
| `instruct` | `true` | Adds deduplication and consent guidance to prompts. |

The default behavior matrix is:

| Mode | `auto_publish` | `report` |
| --- | --- | --- |
| `local` | either | Write locally; never publish automatically. |
| `github` | `false` | Write locally; publish only through explicit `publish` after user consent. |
| `github` | `true` | Create the issue directly; use `fallback_local` on failure. |

## Actions

Before every report, search both open issues and local Markdown reports. A
probable duplicate is listed instead of being written; add evidence to the
existing item or rerun `report` with `force=true`.

### `report`

```text
omo plugin trigger bugreport report -- body=<absolute-path> "<title>"
```

There must be one non-empty title and one absolute, readable UTF-8 `body=`
file no larger than 61,440 bytes. It must contain these headings:

```text
## Summary
## Observed behavior
## Expected behavior
## Steps or evidence
## Anonymization check
```

The report adds a title heading and privacy-safe environment metadata. Local
results say `file: <path>; not published; ...`. Non-user/non-CEO callers mail
the CEO the path and consent instruction; the CEO asks the user before using
`publish`.

### `publish`

```text
omo plugin trigger bugreport publish -- <absolute-local-report-path>
```

This user/CEO-only action accepts only an existing report inside the resolved
bugreport directory. It creates the issue with `gh issue create -R`, appends
`Published: <url>` to the local report, and returns `issue: <url> (created)`.
Already-published reports and probable GitHub duplicates are refused. A
failure preserves the local report.

### `search`

```text
omo plugin trigger bugreport search -- "<query words>"
```

Searches open GitHub issues with `gh issue list` and local `*.md` reports.
GitHub auth or CLI failures are reported and local matching continues. Local
matching compares significant title words (at least three characters, common
stop words ignored) against the report title and `## Summary`; 60% overlap is
the probable-duplicate threshold.

### `notice`

```text
omo plugin trigger bugreport notice -- "<message>"
```

The user-only notice action sends an anonymized investigation request to a
living CEO. It does not create a report itself.

All actions are limited to their manifest roles. Reports must omit project
names, paths, repository or branch names, customer data, secrets, and mail
contents.

## Prompt guidance

When `instruct` is enabled, report-capable prompts require `search` first,
the five headings, local-by-default reporting, and explicit user consent
before `publish`. Guidance is idempotent and stays below the prompt append
cap.
