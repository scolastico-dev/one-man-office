# Smart Model Assignment and Weekly Usage Guard Design

## Purpose

`omo` must choose between configured Claude and Codex profiles using current
weekly subscription capacity, and must stop creating more work before a global
weekly-usage ceiling is exceeded. Usage is read from the credentials already
owned by Claude Code and Codex, then fetched from each provider's usage API.
Credentials are read-only inputs: `omo` never prints, logs, refreshes, or writes
them.

## Configuration

Add a global configuration section:

```yaml
usage:
  weekly_limit_percent: 90
```

`weekly_limit_percent` is the maximum allowed used percentage. It defaults to
`90`, must be greater than `0` and no greater than `100`, and is written into
older configuration files by the existing missing-default mechanism.

Add `smart` to the accepted role assignment values:

```yaml
roles:
  developer:
    models: [claude-opus, codex-luna]
    assignment: smart
```

A smart role may contain only profiles resolved as `claude` or `codex` through
the existing explicit-provider/command-name rules. Unsupported profiles make
configuration validation fail with the profile and role names. Other assignment
modes may continue to use custom or Gemini profiles; profiles without a Claude
or Codex usage source are not subject to the subscription ceiling.

## Provider usage sources

Create an `internal/modelusage` package with a narrow client interface so tests
can supply deterministic snapshots. A snapshot contains provider, credential
scope, account-wide weekly used percentage, optional model-specific weekly used
percentages, reset time when present, and fetch time.

For each profile, credential resolution honors that profile's environment:

- Codex reads `CODEX_HOME/auth.json`, defaulting to `~/.codex/auth.json`. It
  extracts `tokens.access_token` and `tokens.account_id` (accepting snake- and
  camel-case fields) and calls the configured/default ChatGPT usage URL. The
  default is `GET https://chatgpt.com/backend-api/wham/usage`, with bearer auth
  and `ChatGPT-Account-Id` when present. `secondary_window.used_percent` is the
  account weekly percentage; matching additional rate limits are optional
  model-specific percentages.
- Claude reads `CLAUDE_CONFIG_DIR/.credentials.json`, defaulting to
  `~/.claude/.credentials.json`, and extracts `claudeAiOauth.accessToken` plus
  its scopes. On macOS only, a missing file may use a non-interactive read of
  the `Claude Code-credentials` Keychain item. It calls
  `GET https://api.anthropic.com/api/oauth/usage` with bearer auth and
  `anthropic-beta: oauth-2025-04-20`. `seven_day.utilization` is the account
  weekly percentage; provider model-scoped weekly windows are optional
  model-specific percentages.

HTTP calls use a bounded context and redact response bodies from user-facing
errors when they could contain sensitive data. Credential files and native
credential stores are never modified. Distinct profiles that resolve to the
same provider credential scope share one account fetch during a selection.

The effective used percentage for a profile is the greater of the account-wide
weekly percentage and a model-specific percentage that unambiguously matches
the model selected by that profile's CLI arguments. If no unambiguous match
exists, the account-wide percentage is used.

## Startup preflight

After configuration loading and before claiming the office instance lock,
opening or migrating SQLite, running recovery, or spawning the CEO, startup
fetches usage for every distinct Claude/Codex credential scope referenced by a
role. This safety preflight is not disabled by `--skip-startup-checks`.

Missing credentials, missing required scope, an unauthorized response, an
invalid response, a timeout, or any other failed usage fetch exits startup with
an actionable error naming the affected profiles and provider. A successful
fetch at or above the configured ceiling is not a preflight failure; it is a
valid snapshot handled by spawn policy.

Mock mode replaces role profiles before the preflight and therefore performs no
provider credential or network access.

## Selection and ceiling behavior

Every configured spawn attempts a fresh usage fetch for all metered candidate
profiles before choosing one. On a successful refresh, profiles at or above
`weekly_limit_percent` are ineligible.

- `smart` chooses the eligible profile with the lowest effective used
  percentage, which is the highest remaining weekly capacity. Equal
  percentages preserve configuration order.
- `round_robin` rotates over eligible profiles in configuration order.
- `random` chooses only from eligible profiles.
- `failover` starts at its retry-derived position and advances to the next
  eligible profile without moving backward.
- Explicit job model overrides keep their explicit profile and never silently
  substitute another profile.

The existing retry and explicit-selection rules otherwise remain unchanged.
Concurrent spawns serialize refresh/selection state so duplicate requests do
not race round-robin counters or failure notifications.

If a runtime fetch fails after the successful startup preflight, that spawn is
allowed to continue using round-robin over the role's full configured list.
The first failure in a consecutive failure streak sends one durable high
priority message to `user` and appends an event containing the provider and
redacted error. Further failures in the same streak do not send more mail. Any
successful refresh clears the guard so a later failure streak can notify once
again.

## Explicit override and force flow

Add `--force` to `omo job create` and a matching boolean to the socket request
and durable job row. When `--model` names a metered profile, job creation fetches
its current usage. If the effective percentage is at or above the ceiling and
`--force` is absent, the command is rejected before a job row is created. The
error includes the profile, current used percentage, configured ceiling, and
the exact instruction to rerun with `--force`.

`--force` applies only to that job's explicit model selection and permits its
spawn above the ceiling. It does not disable limits for child jobs or other
roles. The flag is persisted so restart/requeue behavior retains the approved
override. If a non-forced queued explicit job crosses the ceiling between
creation and dispatch, dispatch marks it failed with the same actionable note
and informs the user rather than spawning it or silently changing its model.

The guided Commands TUI exposes this flag and warning through Cobra metadata as
part of its own branch; this branch owns the CLI, protocol, and persistence
behavior.

## Automatic safe shutdown

When a successful refresh shows that every configured metered profile for the
role being spawned is at or above the ceiling, and there is no applicable
explicit forced override, no agent is started. `omo`:

1. appends a durable `weekly_usage_limit_reached` event listing each profile's
   used percentage and the configured ceiling;
2. sends a durable urgent message to `user` explaining why no eligible model
   remains and that safe shutdown is starting; and
3. invokes the existing safe-shutdown workflow so living agents finish or save
   handoffs before the office stops.

Shutdown triggers only when every candidate for the role is metered and capped.
An unmetered custom or Gemini profile remains eligible under its existing
assignment behavior and therefore prevents usage-driven shutdown. A capped
explicit override does not trigger office-wide shutdown while another profile
for that role remains eligible; it follows the reject/fail flow above. The
existing `sync.Once` safe-shutdown guard prevents duplicate initiation.

## Reload behavior

Configuration reload validates `smart` roles and the ceiling, then preflights
any newly introduced credential scope before publishing the new configuration.
A failed preflight rejects the reload and leaves the current configuration
active. A changed ceiling applies to the next spawn.

## Testing and documentation

Tests use temporary credential files and `httptest.Server`; they never read or
write the developer's real credentials and never contact provider APIs.
Coverage includes credential path/environment resolution, response decoding,
redaction, strict startup failure, each assignment mode over eligible profiles,
stable smart ties, account/model effective percentages, once-per-streak
fallback notification, force persistence and rejection text, reload rollback,
and automatic safe shutdown.

Update the README configuration example, assignment documentation, CLI
reference, usage guidance, and `CLAUDE.md` configuration invariants. The full
`make check` gate remains required.
