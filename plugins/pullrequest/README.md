# Pullrequest plugin

`pullrequest` is an optional Git-installed plugin for jobs whose effective
`merge_target` is `asis`. It inspects the named integration branch, finds an
existing open request by branch or head commit when possible, pushes the
named integration branch to its explicit remote ref, creates or updates a pull
request or merge request, and mails the result to the user and CEO. Every
request uses an authored Markdown description file; repeating the action is
safe and idempotent.

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

In `auto` mode detection checks GitHub first, then GitLab (`gitlab.com` and
`gitlab_hosts`), and finally probes unknown hosts at Forgejo's
`/api/v1/version`. An explicit `forge` value skips host detection.

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

The action validates the description, then inspects the named integration
branch with `git rev-list --count <base>..<branch>` before any push or provider
authentication. A zero count is a successful no-change result and does not
push, resolve credentials, or call a forge. It reports:

```text
repo: no changes on <branch>; nothing to open
```

For a changed branch, the action looks for an open request by the named branch
and by the branch's current HEAD commit. A matching head commit on a different
branch is reported as `existing`; it is not pushed, edited, or used to create a
duplicate request. Only a request for the named branch is updated. New and
updated requests use the validated description for GitHub CLI, GitHub REST,
Forgejo/Gitea REST, and GitLab form requests. Existing open requests replace
their body and, only when a title was supplied, their title: `gh pr edit` is
used for GitHub CLI, numbered GitHub/Forgejo/Gitea requests receive a PATCH,
and the numbered GitLab merge request receives a PUT. Update status and the
retained request URL are checked before reporting success.

The push uses an explicit `<branch>:<branch>` refspec. This keeps a worktree whose
current branch differs from `integration_branches[].branch` aligned with the
named integration branch and prevents a stale local branch from being pushed.
Git and GitHub CLI failures include the rendered failing command and captured
command output; provider credentials and authorization headers are never
included.

Every repository produces exactly one labeled result line, including a
single-repository run:

```text
repo: URL (created)
repo: URL (updated)
repo: no changes on <branch>; nothing to open
```

The same lines appear in the result mail and plugin log. The manual hook keeps
the aggregate display string in `data.result` and exposes URL-bearing records
in `data._omo_pull_requests` as `{repo,url,state,branch,base_branch,title}`.
No-change repositories are intentionally absent from that structured array, so
they do not create durable pull-request records. A no-change-only run never
claims that a request was created or updated.

For a product manager, trusted `integration_branches` metadata includes only
durable repositories whose effective policy is `asis`; the action processes
one request per entry by default. Pass `repo=<key>` to restrict it to one
entry. Unknown selectors fail with the valid keys. The configured `remote` is
used for the explicit push before provider creation or update.

When `instruct` is enabled, product-manager prompts explain how to author the
six-section description from merged child-job results and review notes in
`storage`. Developer and freelancer prompts give equivalent content guidance
for a worktree or temporary path. They run the action once with
`body=<absolute-path>` before `omo done` and include every returned URL and
label in the done result.
