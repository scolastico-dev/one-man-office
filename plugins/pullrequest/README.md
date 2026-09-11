# Pullrequest plugin

`pullrequest` is an optional Git-installed plugin for jobs whose effective
`merge_target` is `asis`. It pushes the job branch, finds an existing open
request when possible, creates a pull request or merge request, and mails the
URL to both the user and the CEO. Existing open requests are reused, so
repeating the action is safe.

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
| `gitlab_hosts` | `[]` | Additional Git hostnames that should be recognized as GitLab in `auto` mode. `gitlab.com` is always recognized. |
| `token` | `""` | API token. It takes precedence over `token_env`; it is never written to plugin logs or error messages. |
| `token_env` | `""` | Environment-variable name from which to read the token when `token` is empty. |
| `instruct` | `true` | Adds the as-is workflow instruction to PM, developer, and freelancer prompts when enabled. |

In `auto` mode detection checks GitHub first, then GitLab (`gitlab.com` and
`gitlab_hosts`), and finally probes unknown hosts at Forgejo's
`/api/v1/version`. An explicit `forge` value skips host detection.

For GitHub, an authenticated `gh` installation is preferred. The plugin runs
`gh auth status`, then uses `gh pr list`/`gh pr create`; when that check is not
successful it uses the GitHub REST API. REST mode needs a token with the
`repo` scope for private repositories, or `public_repo` for public repositories
(`repo` is the simple choice when both are possible).

Forgejo and Gitea API mode uses `Authorization: token ...` and needs a token
with repository read/write access, including pull-request permission. GitLab
uses `PRIVATE-TOKEN` and needs an API token with the `api` scope (a project
access token may be used with that scope). If `token` is empty, `token_env`
names the environment variable to read; the value is never included in
notifications, plugin logs, errors, or audit data.

The manual action returns the newly created or existing request URL to
`omo plugin trigger` and also sends that URL to the user and CEO. For a
product manager, trusted `integration_branches` metadata creates one request
per entry by default and returns a bounded, repository-labelled list of URLs
with one aggregate notification to each recipient. Pass `repo=<key>` as the
first argument to restrict the action to one integration entry; an optional
title may follow the selector. Unknown selectors fail with the valid keys.
GitHub's authenticated `gh` path and all REST adapters check for an existing
open request before creating one. Bare-remote pushes use the configured
`remote` and branch before provider lookup.

When `instruct` is enabled, an `asis` prompt for a developer or freelancer
asks the agent to run:

```text
omo plugin trigger pullrequest create -- "<title>"
```

before `omo done` and include the returned request URL in the done result.
For a product manager with integration branches, the same action is requested
once after all repository work is complete; the default action covers every
integration entry and `repo=<key>` selects one.
