# Pause notice plugin

The "Paused" switch in the top bar (left of "Stop all agents") stops omo from
starting new agents; agents that already run go on working. This plugin makes
the pause reach them too:

- When you switch the pause on (`omo_paused`; "Stop all agents" switches it on
  as well), every running agent gets a message to halt: stop what it is doing,
  start nothing new and wait until omo says the pause is over.
- When you switch the pause off (`omo_resumed`), every agent that got the halt
  message and still runs gets a message that the pause is over and it should
  continue where it stopped.

It is an official plugin of omo's list: install it on the Plugins page under
"Available plugins". It installs from the `release` branch of omo's
repository, folder `plugins/pause-notice`. It needs no configuration.

## Who gets the messages

Every agent omo runs when the pause is switched on: the CEO, product
managers, developers, reviewers and export jobs. The CEO gets its own text,
because it does not wait with `omo ai wait` and is not checked for silence:
it ends its turn. Agents of tickets are told to wait with `omo ai wait`, so
omo's check for silent agents sees them as parked and does not remind them to
carry on, and the resume message reaches them as the answer of that wait.

An agent that starts while omo is paused gets no halt message and no resume
message: while paused omo starts no agents itself, so such an agent is one you
started (opening the CEO), or one whose start was already under way when you
switched the pause on. "Stop all agents" ends every agent but the CEO, so
after it only the CEO is told to halt.

## How a message reaches an agent

The way omo's own notifications do (a mention, an answer to a question): one
line that starts with `omo: `, typed into the agent's terminal and submitted.
It is not a comment; nothing is written to a ticket. In detail:

- An agent that is busy (in the middle of a turn) gets the line as typed
  input: the agent CLI keeps it until it can read it, usually at the next step
  of its turn. The halt therefore takes effect at the agent's next step, not in
  the middle of a running command.
- An agent parked in `omo ai wait` gets the line as the answer of the wait.
- An agent that is still starting gets it together with its first prompt.
- omo holds the line back while you type into that agent's terminal (up to
  two minutes), so it never ends up inside your own text.

Line breaks in a configured text become spaces; a message is cut at 1,000
characters.

## Configuration

| Key | Meaning |
|---|---|
| `halt_message` | Sent to every agent but the CEO when the pause is switched on. |
| `ceo_halt_message` | Sent to the CEO when the pause is switched on. |
| `resume_message` | Sent to every agent that got a halt message, when the pause is switched off. |

An empty text uses the default. The defaults (omo puts `omo: ` in front):

- `halt_message`: "the user paused omo. Stop what you are doing now: do not
  finish the current step, start nothing new, change no files, run no other
  commands and write no comments. Then wait: run omo ai wait --timeout 60m,
  and run it again whenever it ends without news, until omo tells you that the
  pause is over."
- `ceo_halt_message`: "the user paused omo. Stop what you are doing now: do
  not finish the current step, start nothing new, change nothing on the board,
  run no commands and write no comments. End your turn and do nothing until
  omo tells you that the pause is over."
- `resume_message`: "the pause is over. Continue where you stopped when omo
  was paused."

The plugin keeps the terminals it told to halt in its store (key `halted`)
until the pause is switched off. Its log says how many agents it told and
which ones it could not reach.
