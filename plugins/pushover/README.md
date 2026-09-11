# Pushover plugin

This optional official Lua plugin, installed from Git, sends a Pushover notification when unread office mail
has remained unchanged for the configured stability window. It is a reference
for a scheduled Lua hook, a role-gated manual action, and a mutable
`prompt_render` hook. It is not embedded or installed automatically.

## Install

Install it for the current office:

```bash
omo plugin install https://github.com/scolastico-dev/one-man-office.git --name pushover --subpath plugins/pushover --branch release
```

Or install it globally for all offices:

```bash
omo plugin install https://github.com/scolastico-dev/one-man-office.git --name pushover --subpath plugins/pushover --branch release --global
```

Enable it with the required credentials:

```yaml
plugins:
  installed:
    pushover:
      enabled: true
      config:
        user_key: "your-pushover-user-key"
        app_token: "your-pushover-application-token"
```

`user_key` and `app_token` are required. The plugin also supports its manifest
defaults for `api_url`, `check_interval`, `stable_window`, `priority`, and
`sound`; restart the office after changing plugin configuration.

The cron hook uses `interval_config: "check_interval"` with a `1m` fallback.
The `notify` action accepts callers with the `user` or `ceo` role and requires
one message argument plus an optional title:

```text
omo plugin trigger pushover notify -- "The deployment needs a decision"
omo plugin trigger pushover notify -- "The deployment needs a decision" "Deployment blocker"
```

The action runs immediately. Its title starts with `omo user` or `omo CEO`,
followed by the optional title. Messages are limited to 1024 UTF-8 characters
and titles to 250 UTF-8 characters.

## Inbox cycle

Each cron tick snapshots the unread message IDs as a deterministic set. An
empty inbox clears the cycle. The first nonempty snapshot starts the stability
window without sending. Any ID change before notification restarts the full
window. When the set stays unchanged through `stable_window`, one notification
is attempted; the cycle is marked notified only after a 2xx response.

After a successful notification, changes while the inbox remains nonempty do
not produce more notifications. Only an empty inbox resets the cycle and
allows the next nonempty set to notify.

The body begins with the unread count and office path, followed by at most five
`from <sender>: <subject>` lines. A failed request remains eligible for a later
retry while the set stays stable. Non-2xx responses log only their status and a
parsed response `errors` array, or `errors unavailable` when it cannot be
safely parsed.

## Secret hygiene

Credentials are sent only as the Pushover form's `token` and `user` fields.
They are never included in logs, durable event details, Lua errors, titles, or
prompt text. Missing credentials produce one fixed cron notice per office
startup; the manual action returns a fixed sanitized error.

When the plugin is installed, the CEO prompt includes a note pointing to the
manual trigger for blockers and decisions that need the user. Other roles do
not receive that note.
