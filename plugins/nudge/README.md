# Nudge plugin

This bundled plugin is installed into `.omo/plugins/nudge` when missing. It is
also a complete example of an omo Lua plugin:

- `agent_start` and `agent_log_line` hooks record recent activity in durable,
  plugin-local storage.
- A configurable `cron` hook reads the safe `event.data.agents` snapshot.
- Each cron pass removes activity and cooldown keys for agents that are no
  longer live, using the prefix-filtered `omo.local_keys()` API.
- `omo.exec` runs `omo type ... --key enter` against the same office to submit
  each reminder directly to the target agent's terminal without creating mail.
- `omo.log` records the latest reminder result for the Plugins tab.

The reminders cover unread mail, smoke alarms that forgot `omo done`, retained
agents that forgot `omo wait`, idle agents without a job, reviewers parked
during rework, generally stale work with no recent status, and freelancers
that remain waiting past five minutes. Freelancer-waiting reminders go to the
CEO and explain that a finished retained freelancer must be explicitly ended;
other status, completion, and parking nudges apply directly to worker roles.

Tune the scheduler, activity sampling, reminder thresholds, and repeat periods
under `plugins.installed.nudge.config` in `.omo/omo.yaml`; duration values use
Go syntax such as `30s`, `5m`, or `1h30m`. Restart the office after changing
plugin configuration. `plugin.json` declares these values in `default_config`;
plugin install/update adds missing fields while preserving existing settings.
Ordinary setup and startup never overwrite an existing
copy. When a newer embedded generation is available, interactive startup asks
before `omo setup --update` replaces local bundled-plugin edits. Set
`plugins.installed.nudge.enabled: false` (or run
`omo plugin disable nudge`) to keep it installed but disable its hooks.
