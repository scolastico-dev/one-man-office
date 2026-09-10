# Complete CLI reference

There are two kinds of command:

- **Standalone commands** work in your normal shell.
- **Live-office commands** use `OMO_AGENT_ID` and `OMO_SOCKET` inside agent
  terminals. When run from the active office directory without those
  variables, supported inspection and management commands connect as the
  reserved `user` identity. Every caller remains subject to server-enforced
  role permissions.

You can issue a shared command as an agent by peeking that agent in the TUI,
enabling input with `Ctrl+T`, and typing the command there, or by selecting
that agent's identity in the TUI's Commands tab.

![Guided command console in the TUI](../.github/assets/commands.png)

Every command accepts `-h` / `--help`. `omo help [command]` shows the same
command help, and `omo -v` / `omo --version` prints the installed version.

## Commands for the user

These are the normal entry points expected to be run directly from your shell.

| Command | Arguments and flags | Purpose |
|---|---|---|
| `omo` | `--mock`, `--no-tui`, `--safe-mode`, `--skip-startup-checks`, `--read-only`, `--trust-office` | Start the office. `--mock` uses scripted agents; `--no-tui` runs headless until `Ctrl+C`; `--safe-mode` starts only the CEO until spawning is resumed; `--skip-startup-checks` suppresses release/embedded-asset/plugin update checks once; `--trust-office` approves the current location without a prompt. `--read-only` opens a non-mutating concurrent observer and is incompatible with the mutating startup modes. |
| `omo setup [dir]` | Optional destination directory; defaults to `.`. `--agent-cli auto\|claude\|codex\|gemini` overrides automatic CLI selection; `--non-interactive` skips the setup form; `--with-git` enables portable, commit-ready office handoffs. | Create or complete an office. Interactive terminals show a form with the detected providers and role/assignment defaults. |
| `omo setup --update [dir]` | Optional existing office directory; defaults to `.` | Replace `.omo/messages`, `.omo/prompts`, and bundled plugin directories with this binary's defaults, then refresh the generation marker. |
| `omo setup --sync [dir]` | Optional existing office directory; defaults to `.`. Incompatible with `--update` and explicit `--agent-cli`. | Reapply only `OMO_HOME/template/.omo/omo.yaml` as a strict partial config override. |
| `omo supervisor` | `--listen 127.0.0.1:8090`, `--max-agents 12`, `--usage-cache-ttl 10m`, `--mock`, `--unsafe`, `--basic-auth USER:PASSWORD`, `--no-origin-check`, `--detached` / `-d` | Open the local [browser supervisor](browser-supervisor.md). `--unsafe` disables dashboard token authentication; Basic auth and disabled Origin checks are trusted-network/reverse-proxy options. |
| `omo supervisor stop` | None | Stop this `OMO_HOME`'s supervisor and wait for owned office/shell cleanup. |
| `omo supervisor autostart register` | Same dashboard flags as `omo supervisor` (without `-d`) | Save these exact settings and enable autostart at the current user's next login. |
| `omo supervisor autostart unregister` | None | Remove this home's login entry and saved arguments without stopping the current supervisor. |
| `omo self-update` | `--check`, `--version <tag>` | Install the latest release when newer, report availability, or install an exact release. See [manual self-update](running.md#manual-self-update). |
| `omo repo list` | None | List repository names and absolute paths from `.omo/omo.yaml`. |
| `omo repo add [name] <path>` | A Git checkout; name defaults to its directory name | Add a repository or update an existing entry. Relative paths are normalized to absolute paths. |
| `omo repo remove <name>` | A configured repository name | Remove a repository from the office configuration. |
| `omo plugin list` | `--global` | List configured plugins and their enabled state, locally or in the user-wide home. |
| `omo plugin install <url>` | Optional `--name`, `--subpath`, `--branch`, `--global` | Clone a plugin into `.omo/plugins` (or the global home) and add an enabled entry with manifest defaults to the configuration. |
| `omo plugin update [name]` | Optional configured plugin name, `--global` | Fast-forward one plugin or all managed plugins, refresh active copies, and add missing manifest config defaults. |
| `omo plugin enable <name>` / `disable <name>` | A configured plugin name, `--global` | Toggle loading on the next office start without deleting configuration or files. |
| `omo plugin actions [plugin]` | Optional loaded manifest name | List enabled manual action names, descriptions, and argument support in the running office. |
| `omo plugin trigger [--global] <plugin> <action> [-- <args>...]` | Loaded manifest and action names; arguments require `manual_args: true` on the selected hook | User-only: run the named manual action and wait for completion. Run from the office directory, or use `--global` without a live office. |
| `omo export statistics` | Optional `--output <file>` | Export aggregate row counts, job states, and role counts without project or job details. |
| `omo export db <table\|all>` | Optional `--output <file>` | Export one safe-listed SQLite table or all safe-listed tables as JSON. |
| `omo export git` | None | Write durable specs and active/completed job handoffs under `.omo/specs` and `.omo/jobs`; Git-integrated offices run this automatically during shutdown. |
| `omo export import <job.yaml>` | An exported external job file | Import a handoff as a queued local job, clear its previous assignee, retain its checkpoint, and notify the CEO. |
| `omo completion <shell>` | `bash`, `fish`, `powershell`, or `zsh`; each accepts `--no-descriptions` | Print a shell-completion script to standard output. |
| `omo --help` | Also `omo <command> --help` | Show the command tree or help for one command. |
| `omo --version` | Short form: `-v` | Print the `omo` version. |

## Commands for both the user and agents

These inspect or operate a running office. You may run them directly from the
active office directory; agents use the injected office connection. Permission
notes are enforced by the supervisor, regardless of who is typing.

| Command | Arguments and flags | Purpose / permission |
|---|---|---|
| `omo office pause` | None | Pause spawning new agents. User, CEO, and firefighter. |
| `omo office resume` | None | Resume spawning. User, CEO, and firefighter. |
| `omo office halt-spawns` | None | Halt new work-agent spawns; queued work and smoke/fire safety monitoring remain active. User, CEO, and firefighter. |
| `omo office resume-spawns` | None | Resume new work-agent spawns. If safe mode is active, this also exits safe mode and boots the full office. User, CEO, and firefighter. |
| `omo agent list` | None | List all living agents with role, lifecycle state, job, and published step. |
| `omo agent kill <name-or-role>` | Exact agent name or role | Permanently stop matching agents and cancel their active work. User, CEO, and firefighter. |
| `omo agent restart <name-or-role>` | Exact agent name or role | Replace matching agent processes without requeueing their jobs or incrementing retries. User, CEO, and firefighter. |
| `omo type <agent-name> [text]` | Optional `--key` values may be repeated or comma-separated | Send literal text and/or special keys to an active agent terminal. Text does not imply Enter; add `--key enter` to submit. User, CEO, firefighter, and trusted plugins under the reserved system identity. See [manual agent input](running.md#manual-agent-input). |
| `omo logs <developer-name>` | Optional `-n` / `--lines` (default `100`, maximum `10000`) | Print the latest readable transcript lines for an active developer. User, CEO, and firefighter. |
| `omo reload` | None | Validate and reload `.omo/omo.yaml` in the running office without killing current agents. User, CEO, and firefighter. |
| `omo estop` | None | Immediately stop the office. User, CEO, and firefighter. |
| `omo safe-shutdown` | None | Halt spawning, ask every agent to finish only when near done or save a concise durable handoff, then stop. User, CEO, and firefighter. |
| `omo job list` | None | List jobs visible in the office queue. |
| `omo job show <id>` | Numeric job ID | Show the complete stored job. |
| `omo job cancel <id>` | Numeric job ID | Cancel a job. User, CEO, and firefighter. |
| `omo job requeue <id>` | Numeric job ID | Requeue a failed or cancelled job. User, CEO, and firefighter. |
| `omo job create` | `--title` and `--role` required; exactly one of `--goal <text>` or `--goal-file <path>`; optional `--model`, `--force`, `--repo`, `--parent`, `--developer-models`, `--force-developer-model` | Queue a `product_manager`, `developer`, or `freelancer` job. `--goal-file` copies the file contents into SQLite. `--repo` is required for developer jobs and optional for freelancer worktrees. `--force` permits only that explicit `--model` above the usage ceiling. User and CEO; PMs may create developer jobs under their enforced model policy. |
| `omo incident resolve <id>` | `--report` required | Resolve an incident and send its report to the user. User, CEO, and firefighter. |
| `omo inbox` | None | List unread mail for the current identity. |
| `omo read <id>` | Numeric message ID | Show one message and mark it read. |
| `omo send [body]` | `-s` / `--subject` required; `-t` / `--to` target; `-p` / `--priority` is `low`, `normal`, `high`, or `urgent` (default `normal`) | Send mail as the current identity. Omit `--to` to broadcast; omit the body argument to read it from stdin. [Routing rules](concepts.md#who-may-talk-to-whom) apply. |
| `omo export ...` | See above | The export commands also work from inside the office. |

## Commands for agents

These drive the orchestration protocol and are normally issued by role prompts
rather than typed by you.

| Command | Arguments and flags | Purpose / permission |
|---|---|---|
| `omo ready` | None | Report that the process is alive and print the common prompt, role prompt, and goal. |
| `omo step "<current status>"` | One non-empty description, at most 500 characters | Publish the agent's current activity to `omo agent list` and the TUI. |
| `omo done [result]` | Optional single result argument | Report goal completion. The CEO cannot finish. A freelancer's first completion closes the job but retains its session for follow-up mail. |
| `omo wait` | Optional `--timeout <duration>` such as `30s` or `5m` | Park until mail, a supervisor release, or the optional timeout. The CEO and smoke alarms cannot wait; smoke alarms finish with `omo done`. |
| `omo context save [summary]` | One concise summary argument, `-f`/`--file <path>`, or stdin; maximum 8000 characters | During safe shutdown, persist completed work, remaining work, blockers, and the exact next step. The matching role/job receives it on the next office run, after which it is deleted. |
| `omo job verdict <id> <merge\|reject>` | Optional `--notes`; required when rejecting | Submit a review verdict. Reviewer identity required. |
| `omo job override <id>` | `--notes` required | Have the owning PM overrule an out-of-scope or nitpicking rejection and direct the retained reviewer to merge. |
| `omo incident create` | `--agent` and `--class` required; optional `--detail`. Class: `stuck`, `looping`, `drifting`, `too-slow`, or `other` | File an incident and request a firefighter. Smoke-alarm or firefighter identity required. |
| `omo branch-name <name>` | A `<type>/<kebab-description>` branch name | Used by the short-lived branch-naming agent to return its choice. |

`omo fake-agent [--scenario <file>] [--auto-role <role>]` is a hidden internal
test command used by `--mock` and the integration suite.
