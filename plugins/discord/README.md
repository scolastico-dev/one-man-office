# Discord plugin

Posts a message to a Discord channel through a channel webhook when

- a ticket needs your attention (`ticket_needs_attention`), or
- an agent asks you a question (`question_asked` with the question addressed
  to the user; questions to other roles send nothing).

It also has a global action, "Send a test message".

It is an official plugin of omo's list: install it on the Plugins page under
"Available plugins". It installs from the `release` branch of omo's
repository, folder `plugins/discord`.

## Setting it up

In Discord, open the channel's settings, Integrations, Webhooks, and create a
webhook. Copy its address and put it into the plugin's configuration as
`webhook_url`, then press "Send a test message".

## Configuration

| Key | Meaning |
|---|---|
| `webhook_url` | The webhook's address, `https://discord.com/api/webhooks/<id>/<token>` (`discordapp.com` and `/api/v<n>/webhooks/` are taken too). Required. Anyone with it can post to the channel: treat it like a password. |
| `events` | Which events send: a list of `ticket_needs_attention` and `question_asked`. Default both. |
| `mention` | Empty, `role:<id>` or `user:<id>`: that role or user is mentioned in every message and is the only one a message may ping. |
| `username` | The name the messages show. Default `omo`. Discord refuses names with "discord" or "clyde"; such a name is left out. |
| `max_per_hour` | At most this many messages per hour. Default `20`. |

The configuration is stored as plain values in the plugin's settings file.
omo removes configuration values (the webhook address among them) from the
plugin's log and error texts.

## What a message says

The text is the mention (if one is set) and "A ticket needs your attention"
or "A question for you". An embed follows: its title is `#<number> <ticket
title>`, its text the attention text or the question, its link the ticket on
the board (omo's advertised address, `public_url`).

Ticket titles, attention texts and questions are written by people and
agents. Every character Discord reads as formatting, a mention, a link or an
emoji gets a backslash, and every message carries `allowed_mentions` with an
empty `parse` list, so text from a ticket can never ping `@everyone`,
`@here`, a role or a user; only the configured mention can. Discord's limits
are kept: 2,000 characters of text, 256 of the embed title, 4,096 of the embed
text; longer text is cut with "…".

The plugin sends only to the webhook address above (https, `discord.com` or
`discordapp.com`); a `webhook_url` of any other form sends nothing and says so
in the log.

## Hourly cap

At most `max_per_hour` messages go out per hour, counted from the first one
of the hour. The one after that is a notice, "further messages are held back
until <time>", and nothing else is sent until the hour is over. The first
message of the next hour writes to the plugin's log how many were held back.
The test action is not counted.

## Failures

A request is tried at most three times. Discord's answer 429 (rate limited)
waits as long as its `Retry-After` header (or `retry_after`) says, a 5xx
answer or a failed connection waits 1 s, then 2 s; a wait over 5 s is not
made. Other answers in the 4xx range are not tried again (401, 403 and 404
mean the webhook is gone or its address is wrong). What failed is written to
the plugin's log; the plugin does not fail the event, and the next event
tries again. The test action shows the outcome as its answer.
