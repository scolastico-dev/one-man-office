# omo documentation

omo (one-man-office) is one server per user with a kanban board in the
browser. You write tickets; AI coding agents (Claude Code, Codex, Antigravity,
Gemini CLI) work them in git worktrees of your repositories, review each
other's work, and omo merges the result or opens pull requests. The
[README](../README.md) is the short overview; this wiki is the manual.

## Start here

- [Getting started](getting-started.md): install, first start, and the first
  ticket from Planning to Done.
- [Concepts](concepts.md): stories and tasks, columns, roles, the CEO,
  approval, review rounds, tasks without a project, limits, pause, restarts.
- [Upgrading from 0.11](upgrading-from-0.11.md): coming from the terminal
  version of omo.

## Working with omo

- [The board](board.md): cards, moving them, filters, creating tickets, the
  ticket panel and page, comments, images, the Diff tab, the sidebar and the
  CEO tab, notifications, keys.
- [Web UI forwards](proxy.md): live dev servers through the board, app base
  paths, optional subdomains, sign-in and HTTPS deployment.
- [Terminals](terminals.md): your shells, typing into agents, copy and paste,
  images, Shift+Enter, terminals in their own window.
- [Merging and export](merging-and-export.md): local merges, merge commit or
  squash, pull requests per repository, branch names.

## Setting it up

- [Agents](agents.md): the agent programs, detection, profiles and models,
  roles and assignment, usage limits and usage scripts.
- [Settings reference](settings.md): every settings page and `settings.yaml`.
- [Skill sets](skills.md): skill repositories for your agents, skills.sh.
- [Plugins](plugins.md): installing plugins, plugin lists, writing a plugin
  (manifest, events, Lua API), the plugins omo ships.
- [MCP](mcp.md): board tools for agents, relaying MCP servers, registering omo
  in the agent programs, access for programs on other machines.

## Running it

- [Running omo](running.md): start and stop, start options, sign-in modes,
  reaching omo from another machine, reverse proxy, start at login, updating,
  Docker.
- [Command line](cli.md): every command with its help text.
- [Security](security.md): what agents can do, what the token protects, what
  plugins and relayed servers can do.
- [Backup and restore](backup.md): settings export and import, the database.
- [Where state lives](home-folder.md): the omo home folder.
- [Troubleshooting](troubleshooting.md): the messages you will meet and what
  to do.

## Contributing

- [Contributing](contributing.md): building, testing and the map of the code.
- [Native UI API](ui-api.md): capability flags, authenticated incremental
  notification polling, delivery cursors and recovery.

Questions and bug reports: the
[issues page](https://github.com/scolastico-dev/one-man-office/issues).
