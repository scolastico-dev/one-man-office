# Pullrequest plugin

`pullrequest` is an optional Git-installed plugin for jobs whose effective
`merge_target` is `asis`. It pushes the job branch, finds an existing open
request when possible, creates or updates a pull request or merge request, and
mails the result to the user and CEO. Every request uses an authored Markdown
description file; repeating the action is safe and idempotent.

The plugin is not bundled into an office automatically. The official catalog
entry is version `1.0.0` from the `release` branch. Install it for one office
with:

```bash
omo plugin install \
  --name pullrequest \
  --subpath plugins/pullrequest \
  --branch release \
  https://github.com/scolastico-dev/one-man-office.git
```

Install it for every office under the current omo home with:

```bash
omo plugin install --global \
  --name pullrequest \
  --subpath plugins/pullrequest \
  --branch release \
  https://github.com/scolastico-dev/one-man-office.git
```

## Configuration

Configure the `plugins.installed.pullrequest.config` object in the office
configuration (or in the global configuration for a global installation):

| Key | Default | Meaning |
| --- | --- | --- |
| `remote` | `origin` | Git remote pushed with `git -C <worktree> push -u <remote> <branch>`. |
| `forge` | `auto` | `auto`, `github`, `forgejo`, `gitea`, or `gitlab`. |
| `api_url` | `""` | API root override. GitHub uses it as supplied; Forgejo appends `/api/v1` and GitLab appends `/api/v4` when those suffixes are absent. |
| `gitlab_hosts` | `[]` | Additional Git hostnames recognized as GitLab in `auto` mode. `gitlab.com` is always recognized. |
| `token` | `""` | API token. It takes precedence over `token_env`; it is never written to plugin logs or error messages. |
| `token_env` | `""` | Environment-variable name from which to read the token when `token` is empty. |
| `instruct` | `true` | Adds authored-description guidance to PM, developer, and freelancer prompts when enabled. |
| `servers` | `[]` | Ordered per-host settings. The first normalized host match supplies the provider settings for that repository. |

Each `servers` entry has a required `host` and these optional string fields:

| Field | Default when matched | Meaning |
| --- | --- | --- |
| `host` | required | Hostname to match after trimming and lowercasing. Hosts must be unique after normalization. |
| `forge` | `auto` | `auto`, `github`, `forgejo`, `gitea`, or `gitlab`. |
| `api_url` | `""` | Provider API root override, with the same provider-specific suffix rules as the flat key. |
| `token` | `""` | Entry token; it takes precedence over this entry's `token_env`. |
| `token_env` | `""` | Environment-variable name for this entry's token. |
| `remote` | flat `remote`, ultimately `origin` | Git remote pushed and used to determine the provider owner, project, and host. |

For example, one installation can serve GitHub and Forgejo repositories with
different credentials:

```yaml
plugins:
  installed:
    pullrequest:
      config:
        servers:
          - host: github.example
            forge: github
            api_url: https://api.github.example
            token_env: PULLREQUEST_GITHUB_TOKEN
          - host: forge.example
            forge: forgejo
            api_url: https://forge.example/api/v1
            token_env: PULLREQUEST_FORGEJO_TOKEN
```

Entries are checked in array order. Matching trims and lowercases both the
configured host and the parsed Git remote host, after the parser removes URL
userinfo and a numeric port; for example,
`ssh://git@FORGE.EXAMPLE:2222/acme/repo.git` matches `forge.example`.
Hosts that do not match any entry use every flat setting exactly as before.
For a match, `forge`, `api_url`, `token`, and `token_env` come only from that
entry, with the defaults shown above; they do not inherit those flat values.
An entry token wins over its `token_env`, and tokens are redacted from errors,
durable output, logs, and notification mail.

Remote selection starts with the flat `remote` (or `origin`) to find the
matching host. If the matched entry supplies a non-empty `remote`, that named
remote is then read and parsed and is used for the push and provider owner,
project, and host. An omitted or empty entry `remote` falls back to the flat
remote. Other configured entries' remotes are not read.

In `auto` mode detection checks GitHub first, then GitLab (`gitlab.com` and
`gitlab_hosts`), and finally probes unknown hosts at Forgejo's
`/api/v1/version`. An explicit `forge` value skips host detection.
`gitlab_hosts` is global: it remains a flat setting used only when the
effective forge is `auto`; it is not inherited into or configured per entry.

For GitHub, an authenticated `gh` installation is preferred. The plugin runs
`gh auth status`, then uses `gh pr list` and `gh pr create` or `gh pr edit`; when
that check is not successful it uses the GitHub REST API. REST mode needs a
token with the `repo` scope for private repositories, or `public_repo` for
public repositories. Forgejo and Gitea use `Authorization: token ...` and
GitLab uses `PRIVATE-TOKEN`; their tokens need repository/API write access.

## Description files

The exact manual syntax is:

```text
omo plugin trigger pullrequest create -- [repo=<key>] body=<absolute-path> "<title>"
```

`repo=<key>` and `body=<absolute-path>` may appear in either order. The title
is optional and may be supplied once. `body=` is required and must be an
absolute POSIX path, Windows drive-root path, or UNC path. Relative paths are
rejected; the manual event does not provide a trusted caller CWD, so the
plugin never guesses a resolution from `worktree`. PM descriptions belong in
`storage`; developers and freelancers may use their worktree or a temporary
path, never another location inside `.omo`.

The file is read as bytes with `io.open(..., "rb")`. Its raw size must be at
most 61,440 bytes (60 KiB), and it must be valid UTF-8 and non-empty after
trimming. Only trailing whitespace at end of file is removed; all other bytes
are preserved. It must contain these level-two headings, case-insensitively:

```text
## Summary
## What changed
## Why
## How it was verified
```

`## Risks and follow-ups` and `## Jobs` are recommended. Usage, file, UTF-8,
size, and heading errors report the exact fault, the correct invocation, all
required and recommended sections, and this description-file section. They
fail before Git push, forge probing, credential resolution, or provider HTTP
requests. No fallback body is generated. When a non-user agent calls the
action, the same guidance is best-effort mailed to that caller; user callers
receive the synchronous error only. Mail failure never replaces the original
usage error.

## Create and update behavior

The action pushes only after description validation. New requests use the
validated description for GitHub CLI, GitHub REST, Forgejo/Gitea REST, and
GitLab form requests. Existing open requests replace their body and, only
when a title was supplied, their title: `gh pr edit` is used for GitHub CLI,
numbered GitHub/Forgejo/Gitea requests receive a PATCH, and the numbered
GitLab merge request receives a PUT. Update status and the retained request
URL are checked before reporting success.

Every repository produces exactly one labeled result line, including a
single-repository run:

```text
repo: URL (created)
repo: URL (updated)
```

The same created/updated state appears in the success mail and plugin log. For
a product manager, trusted `integration_branches` metadata includes only
durable repositories whose effective policy is `asis`; the action processes
one request per entry by default. Pass `repo=<key>` to restrict it to one
entry. Unknown selectors fail with the valid keys. Bare-remote pushes use the
configured `remote` and branch before provider lookup.

When `instruct` is enabled, product-manager prompts explain how to author the
six-section description from merged child-job results and review notes in
`storage`. Developer and freelancer prompts give equivalent content guidance
for a worktree or temporary path. They run the action once with
`body=<absolute-path>` before `omo done` and include every returned URL and
label in the done result.
