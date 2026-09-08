# one-man-office wiki

Reference documentation for `omo`. The [README](../README.md) covers the
introduction, installation, and quick start; everything else lives here.

## Using omo

- [Core concepts](concepts.md): the office, roles, jobs, merges, mail routing, and restart recovery.
- [Running an office](running.md): start modes, safe mode, read-only observation, startup checks, self-update, safe shutdown, logs, and manual agent input.
- [The TUI](tui.md): peek, overview tabs, the command console, and keyboard controls.
- [Configuration](configuration.md): the complete `.omo/omo.yaml` reference, model profiles, usage limits, and recommended model choices.
- [Global home and office trust](global-home.md): the user-wide `omo` home, trusted offices, new-office templates, and shared extensions/plugins.
- [Complete CLI reference](cli.md): every command for users and agents with its permissions.
- [Git integration](git-integration.md): committing an office with `--with-git`, job handoff exports, and imports.

## Customizing omo

- [Prompts, messages, and extensions](prompts.md): how agent instructions are assembled and how to change them safely.
- [Writing plugins](plugins.md): manifests, events, Lua and command hooks, configuration, manual actions, storage, and distribution.

## Hosting and development

- [Browser supervisor](browser-supervisor.md): the local web dashboard for several offices.
- [Docker](docker.md): container images, Compose, and agent CLI provisioning.
- [Development and testing](development.md): building from source, running the suite, and the fake agent.
