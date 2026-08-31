# Nudge plugin

This bundled plugin is installed into `.omo/plugins/nudge` when missing. It is
also a complete example of an omo Lua plugin:

- `agent_start` and `agent_log_line` hooks record recent activity in durable,
  plugin-local storage.
- A one-minute `cron` hook reads the safe `event.data.agents` snapshot.
- `omo.nudge` sends a durable system message through normal office mail, so a
  waiting agent wakes and active terminal input keeps its debounce protection.

The reminders cover unread mail, smoke alarms that forgot `omo done`, retained
agents that forgot `omo wait`, idle agents without a job, reviewers parked
during rework, and generally stale work with no recent status.

Edit the installed copy to tune messages or thresholds. Setup and update never
overwrite an existing copy. Set `plugins.installed.nudge.enabled: false` in
`.omo/omo.yaml` (or run `omo plugin disable nudge`) to keep it installed but
disable its hooks.
