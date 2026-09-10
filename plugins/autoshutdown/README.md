# Autoshutdown plugin

`autoshutdown` is an optional office-local Lua plugin. It requests an
orderly `omo safe-shutdown` after the office has been quiet for the configured
period. It is not embedded in `omo` and is never installed automatically;
copy or install this plugin and enable it in the office configuration before
use.

## Configuration

The manifest defaults are:

```yaml
idle_after: 30m
exempt_roles:
  - ceo
  - smokealarm
check_interval: 30s
```

Configure the plugin under `plugins.installed.autoshutdown.config` in the
office's `.omo/omo.yaml`. Duration values use Go syntax such as `30s`, `30m`,
or `1h30m`. Restart the office after changing plugin configuration.

`idle_after` controls both the startup grace period and the required quiet
period. `check_interval` controls how often the cron hook checks the office.
Living agents whose roles are not in `exempt_roles` reset the quiet period;
the CEO and smoke alarm are exempt by default. A newer CEO activity timestamp
also resets the quiet period. Agent creation times are not used to determine
office startup or idleness.

The plugin reconciles its local state with `office_started_at_unix`, so a
restart preserves an in-progress quiet interval while a genuinely new office
session starts over. If an orderly shutdown is already in progress, the
plugin records that state and remains quiet. A successful request is recorded
and is not repeated until active work or a new office session resets it.

When the quiet interval completes, the plugin requests the existing orderly
shutdown path and never invokes `omo estop`:

```text
autoshutdown: office idle for 30m (no active agents, no CEO activity)
```

The duration in that reason is replaced by the configured `idle_after` value.
Countdown/status logs are throttled to no more than one per five minutes.
Failed shutdown requests are logged with sanitized error context and remain
retryable on a later cron tick.
