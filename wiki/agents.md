# Agents

An agent is an AI coding program that omo starts in a terminal for a role on a
ticket. omo knows four programs and can run any other through a custom
profile. This page covers which program runs with which model, and how omo
keeps agents off accounts that are nearly used up.

- [Live web UIs](#live-web-uis)
- [The agent programs](#the-agent-programs)
- [Detection](#detection)
- [Profiles and models](#profiles-and-models)
- [Roles and assignment](#roles-and-assignment)
- [Usage](#usage)
- [Usage scripts](#usage-scripts)

## Live web UIs

Developer, reviewer and product-manager default prompts teach agents to register
a [Web UI forward](proxy.md) whenever they start an app the user should inspect.
They register before starting it, configure path mode with the returned base
path, and name the forward in their hand-in. Registration never starts the
process. Each role keeps its existing filesystem/review restrictions.

Forwards default to the agent's own ticket and disappear when that ticket
completes or is cancelled. A developer's task preview does not survive the
task's merge into its story; a PM's own forward belongs to the story.

## The agent programs

| Program | Command | Account folder variable | Skips permission prompts with |
|---|---|---|---|
| Claude Code | `claude` | `CLAUDE_CONFIG_DIR` | `--dangerously-skip-permissions` |
| Codex | `codex` | `CODEX_HOME` | `--dangerously-bypass-approvals-and-sandbox` |
| Antigravity | `agy` | none: its login stays in `~/.gemini/antigravity-cli` | `--dangerously-skip-permissions` |
| Gemini CLI | `gemini` | `GEMINI_CLI_HOME` | `--yolo` |

Install and sign in to the programs yourself, as your user, before omo starts
them; omo never installs or signs in a program. Google recommends its
Antigravity CLI (`agy`) in place of Gemini CLI; omo supports both.

**Agents act without asking.** The profiles omo writes start each program with
the argument that skips its permission prompts, because nobody sits at an
agent's terminal to answer them. The agent can then run any command as you.
Read [Security](security.md) before you let agents work on a repository you
care about.

omo prepares each start for the program: it trusts the agent's worktree for
that session (Claude Code's folder trust, Codex and Antigravity through
arguments, Gemini through a variable), gives Claude Code and Codex its
[MCP](mcp.md) entry, and types the start prompt in. Every program omo starts
gets omo's environment without the variables that tie a program to the editor
or terminal omo was started from (`TERM_PROGRAM`, `VSCODE_*`, `TMUX`,
`GIT_ASKPASS` and the like), so an omo started from a VS Code terminal does not
hand that editor to its agents.

`omo` must be on the `PATH` the agents see, because they call `omo ai` by name.

## Detection

At the very first start of a home (no `settings.yaml` and no profile yet), omo
looks for `claude`, `codex`, `agy` and `gemini` on `PATH`, without running
them, and writes a profile for each one found. It also gives models to the
roles that have none:

- CEO: the strongest model of each new profile (failover).
- Product manager: the strongest model agents may choose (super smart).
- Developer: a middle model (super smart).
- Reviewer: the strongest model agents may choose (random).

**Settings → Agents → Detect agent programs** does the same later, for programs
that have no profile yet, and lists what it wrote. It never changes an
existing profile or role.

The models omo suggests (`<profile>/<model>`; the first of each is kept for the
CEO and is not offered to agents for tickets):

| Program | Models |
|---|---|
| Claude Code | `fable`, `opus`, `sonnet`, `haiku` |
| Codex | `astra` (`gpt-6-astra`), `sol` (`gpt-6.1-sol`), `luna` (`gpt-5.6-luna`) |
| Antigravity | `opus` (`claude-opus-4-6-thinking`), `sonnet` (`claude-sonnet-4-6`), `flash-high` (`gemini-3.8-flash-high`), `flash-low` (`gemini-3.8-flash-low`), `gpt-oss` (`gpt-oss-120b-medium`) |
| Gemini CLI | `carbon` (`gemini-4-carbon`), `argon` (`gemini-4-argon`), `hydrogen` (`gemini-4-hydrogen`), `flash` (`gemini-3.8-flash`) |

Antigravity's models come from two quota groups with a weekly limit each
(Claude and GPT models, Gemini models); see [Usage scripts](#usage-scripts).

## Profiles and models

A **profile** (**Settings → Agents**, file `profiles/<name>.yaml`) is one
agent program with one account:

- **Agent program**: Claude Code, Codex, Antigravity, Gemini or Custom.
- **Command**: the program to run; only the name is stored, and `PATH` is
  searched at each start.
- **Base arguments**: every model starts with these. For a Custom program,
  `%prompt%` in the arguments is replaced by the start prompt.
- **Environment**: variables for this profile's agents, used exactly as typed.
- **Account folder**: a second account of the same program (for example a
  second Claude subscription): it becomes `CLAUDE_CONFIG_DIR`, `CODEX_HOME` or
  `GEMINI_CLI_HOME` for this profile's agents. Sign in once with that variable
  set.
- **Usage script**: which script reports this account's usage (see below).
- **Advanced: start-prompt delivery**: how omo hands the program its start
  prompt (by default on, after 3 s, 3 retries every 30 s while the agent shows
  no output).

A profile has one or more **models**, each a name with:

- **Extra arguments** after the base arguments (this is where the program's
  model flag goes, for example `--model sonnet`),
- **Extra environment**,
- **Usage windows that count** (see below),
- **Agents may choose it for tickets they create**,
- **Description**, shown where models are chosen.

Roles and tickets name a model `<profile>/<model>`, for example
`claude/sonnet`. **Add missing models** adds the models omo suggests for the
program that the profile lacks. **Duplicate…** copies a profile under a new
name, the usual way to add a second account. Changes apply to agents started
after the save.

Deleting a profile that roles use asks again and takes its models out of the
roles; a profile whose models open tickets use cannot be deleted.

## Roles and assignment

**Settings → Roles** (files `roles/ceo.yaml`, `pm.yaml`, `developer.yaml`,
`reviewer.yaml`) gives each role an ordered list of models and a way to pick
one:

| Assignment | Picks |
|---|---|
| **Round robin** | each model in turn |
| **Random** | a model at random |
| **Failover** | the first that can start; the next only when it cannot |
| **Smart** | the model with the most capacity left, by the usage readings; without readings like round robin |
| **Super smart** (`super_smart`) | like smart, with increasing preference for capacity whose usage window resets soon |

The default assignment is super smart for the product manager and developer,
random for the reviewer, and failover for the CEO. Existing explicit assignments
are kept.

Super smart scores each eligible metered model by remaining percentage ×
(1 + 24 hours ÷ time until reset), using the window reported for that model
(the fullest window below its limits). Time until reset has a one-second floor.
Missing or elapsed reset times add no bonus; without reset times this is smart.
For example, 80% used with 24 hours left scores 40, while 20% used with 7 days
left scores about 91. After 20 hours, those scores become 140 and about 93,
so the first profile is preferred before its allowance resets. Soft and hard
usage limits still apply. Ties keep the role's order, and unknown or unmetered
models take turns with the best metered model, just as in smart.

With round robin and random, a model listed twice is picked twice as often;
with failover, smart and super smart it counts once. Export
jobs use the developer's models. A ticket can name a model of its own
(**Model** in its details, or `--model`); otherwise its role's list applies.
The top of the Roles page says whether every role has a usable model. The
CEO's model is also chosen in the CEO tab.

## Usage

omo reads how full each account is and keeps agents off models that are close
to their limit. **Settings → Usage → Limits**:

- **Soft limit, %** (90): from here no new agent starts on the model. With
  another model of the role below its limits omo uses that one; with none, the
  card shows "waiting for usage" and starts by itself when a reading drops.
- **Hard limit, %** (98): from here a working agent is asked for a handoff and
  ended ("Stopped: claude/opus reached its usage limit (…)"); the ticket goes
  on with another model of its role or waits for the reset. Parked agents and
  the CEO end at once.
- **Refresh every** (5 minutes).
- **Limits per window**: other limits for one window (`weekly`, for every
  script) or one script's window (`claude/weekly`).

A model's usage is the fullest window that counts for it. **Usage windows
that count** on the model lists them; empty means every window of the script
except `extra_usage` (paid usage beyond the plan). A listed window the account
does not have is ignored.

A model is ok, near its limit (soft), at its limit (hard), unmetered (its
profile has no script) or unknown (no reading yet, or none for 24 hours). An
unknown model stays available. After a failed run, the last readings keep
counting for 30 minutes.

A ticket with a model of its own that waits for usage has **Start anyway…**:
it starts on that model although it is near its limit (not at the hard limit).

The sidebar's Agents tab shows the meters per account, when they reset, and
which models agents are kept off.

## Usage scripts

A usage script is a Lua file, `OMO_HOME/usage/<name>.lua`, that asks a provider
how full an account is. omo runs it once per account (profiles with the same
script, account folder and environment are one account), at start, every
refresh interval and on **Refresh**.

omo ships three scripts. They were written against recorded answers of the
services; omo's tests never call the services. Press **Test** on the Usage page
with your own account before you rely on one.

- `claude` reads Claude Code's login (`.credentials.json` in the account
  folder or `~/.claude`, on macOS also the keychain) and asks
  `api.anthropic.com` for the limits: `session`, `weekly` and `<model>_weekly`
  (`fable_weekly` counts only for `claude/fable`), plus `extra_usage`.
- `codex` reads Codex's login (`auth.json` in `CODEX_HOME` or `~/.codex`),
  refreshes it once when it expired, and asks `chatgpt.com`: `session`,
  `weekly`, and windows of other lengths as `window_<hours>h`.
- `agy` runs `agy -p /usage --output-format json` (no model turn) and reports
  `gemini-weekly` and `3p-weekly`. Antigravity's `opus`, `sonnet` and `gpt-oss`
  count `3p-weekly`, `flash-high` and `flash-low` count `gemini-weekly`.
- Gemini CLI has no script: its models are unmetered.

Scripts run as you, with your accounts' credentials; omo never stores, logs or
shows a token. On the Usage page you can edit a script (**Show default**,
**Reset to omo's default…**), create your own (**New script…**) and **Test** a
draft as a chosen profile without saving.

An edited script replaces omo's for good: a newer omo with a changed default
does not touch it and does not tell you. After an update, compare it with
**Show default**, or **Reset** it.

### Writing a usage script

```lua
function usage(account)
  -- account.profile, account.cli, account.account_dir (empty when none), account.home, account.os
  local text, err = omo.read_file("~/my-usage.json")
  if not text then error("no readings: " .. tostring(err), 0) end
  local data = omo.json_decode(text)
  return {
    { name = "session", used = data.session, resets_at = data.session_resets },
    { name = "weekly",  used = data.weekly, label = "Weekly, all models" },
  }
end
```

The result is a list of at most 32 windows. `name`: lower-case letters, digits,
`-` and `_`, at most 40 characters, once each. `used`: 0 to 100. `resets_at`
(optional): RFC 3339 text or Unix seconds. `label` (optional): what people
read, at most 60 characters. A run ends after 60 seconds; there is no shell and
no other file access.

Helpers, all under `omo.`:

| Helper | What it does |
|---|---|
| `http{method, url, headers, json \| form \| body, timeout}` | https anywhere, http only to localhost; at most 8 calls, 1 MiB per answer, 30 s per call |
| `read_file(path)` | an absolute or `~/` path; regular files up to 1 MiB, never through a link |
| `env(name)` | omo's environment, the plain entries of the Environment page (no command runs), the profile's account folder variable and its environment |
| `keychain(service)` | macOS only |
| `codex_refresh(auth_path)` | Codex profiles: runs the profile's command until the login file changes |
| `agy_usage()` | Antigravity profiles: runs `agy -p /usage --output-format json` |
| `rate_limited(retry_after)` | ends the run as rate limited; omo waits as asked (1 minute to 1 hour), else 2, 4, 8 … minutes |
| `json_decode`, `json_encode`, `sha256_hex`, `nfc`, `time_parse`, `now`, `log` | helpers |

The Usage page lists them under "What a script returns, and the helpers it can
call". A script can send what it reads to any https host: it is your code.
