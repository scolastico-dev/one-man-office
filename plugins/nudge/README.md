# Nudge plugin

This bundled plugin is installed into `.omo/plugins/nudge` when missing. It is
also a complete example of an omo Lua plugin:

- `agent_start` and `agent_log_line` hooks record recent activity in durable,
  plugin-local storage.
- A configurable `cron` hook reads the safe `event.data.agents` snapshot.
- Each cron pass removes activity and cooldown keys for agents that are no
  longer live, using the prefix-filtered `omo.local_keys()` API.
- `omo.exec` runs `omo send` against the same office to deliver each reminder
  through normal durable mail and wake waiting agents.
- `omo.log` records the latest reminder result for the Plugins tab.

The reminders cover unread mail, smoke alarms that forgot `omo done`, retained
agents that forgot `omo wait`, idle agents without a job, reviewers parked
during rework, and generally stale work with no recent status.
The CEO only receives unread-mail reminders; status, completion, and parking
nudges apply exclusively to worker roles.

Tune the scheduler, activity sampling, reminder thresholds, and repeat periods
under `plugins.installed.nudge.config` in `.omo/omo.yaml`; duration values use
Go syntax such as `30s`, `5m`, or `1h30m`. Restart the office after changing
plugin configuration. Ordinary setup and startup never overwrite an existing
copy. When a newer embedded generation is available, interactive startup asks
before `omo setup --update` replaces local bundled-plugin edits. Set
`plugins.installed.nudge.enabled: false` (or run
`omo plugin disable nudge`) to keep it installed but disable its hooks.
