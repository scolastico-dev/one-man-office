# Pushover plugin

Sends a [Pushover](https://pushover.net/) notification to your phone when

- a ticket needs your attention (`ticket_needs_attention`), or
- an agent asks you a question (`question_asked` with the question addressed
  to the user; questions to other roles send nothing).

It also has a global action, "Send a test notification".

This plugin ships inside omo: install it on the Plugins page as the bundled
plugin `pushover` (source `builtin:pushover`). "Update" copies the version of
the running omo again.

## Configuration

| Key | Meaning |
|---|---|
| `user_key` | Your Pushover user key. Required. |
| `app_token` | The token of a Pushover application you created. Required. |
| `api_url` | Where messages go. Default `https://api.pushover.net/1/messages.json`. |
| `priority` | Pushover priority, `-2` to `2`. Default `0`. |
| `sound` | A Pushover sound name. Empty: your default sound. |
| `max_per_hour` | At most this many notifications per hour. Default `20`. |

The configuration is stored as plain values in the plugin's settings file.
omo removes configuration values from the plugin's log and error texts.

## What a notification says

The title is "omo: a ticket needs your attention" or "omo: a question for
you". The message is `#<number> <ticket title>: <attention text or question>`,
cut to Pushover's limit of 1,024 characters, and the notification links to the
ticket on the board. Titles and texts are sent as they were written; nothing in
them is interpreted.

## Hourly cap

At most `max_per_hour` notifications go out per hour, counted from the first
one of the hour. The one after that is a notice, "further notifications are
held back until <time>", and nothing else is sent until the hour is over. The
first notification of the next hour writes to the plugin's log how many were
held back. The test action is not counted.

## Failures

A request that fails (no connection, a timeout, an answer other than 2xx) is
written to the plugin's log with the reason or the status code. The plugin
does not retry and does not fail the event; the next event tries again. The
test action shows the outcome as its answer.
