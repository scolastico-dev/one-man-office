# Getting started

From nothing to a first ticket that an agent finishes and omo merges, in about
fifteen minutes.

## What you need

- Linux, macOS or Windows, on amd64 or arm64. (omo is built and unit-tested
  for Windows, but its end-to-end tests do not run there.)
- git 2.38 or newer.
- At least one agent program, installed and signed in as your user: Claude
  Code (`claude`), Codex (`codex`), Antigravity (`agy`) or Gemini CLI
  (`gemini`). See [Agents](agents.md).
- Your repositories on this machine.
- For exporting pull requests later: `gh`, `glab` or `tea`, signed in.

## Install

Linux and macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.ps1 | iex
```

The installer picks the latest release for your system, checks it against the
release's checksums and installs `omo` into `~/.local/bin`
(`%LOCALAPPDATA%\Programs\omo` on Windows), which it adds to `PATH`. Open a new
shell afterwards. `OMO_INSTALL_DIR` chooses another folder. To read the script
first, download it and run it with `sh install.sh` (`.\install.ps1`).

`omo` must be on the `PATH` of the agents too, because they call `omo ai` by
name.

Other ways: build from source (`make install PREFIX=~/.local`, see
[Contributing](contributing.md)), or [Docker](running.md#docker).

## First start

```bash
omo -d
```

omo starts in the background and prints its address with the login token:

```text
omo: http://omo.localhost:8090/#4f01f6c3…
also: http://127.0.0.1:8090/#4f01f6c3…
Open the address in your browser. The part after # is your login key: keep it to yourself.
```

If this machine has an older omo (0.11 or earlier) in `~/.local/omo`, omo first
lists what that left and asks whether to delete it, go back or quit: see
[Upgrading from 0.11](upgrading-from-0.11.md). Do this in a terminal, not from
a login entry.

Open it in your browser. The token stays in that browser; `omo -d` prints the
address again whenever you need it. `omo stop` stops omo. (See
[Running omo](running.md) for the port, sign-in and start at login.)

At this first start omo looked for `claude`, `codex`, `agy` and `gemini` on
your `PATH` and wrote a profile for each one it found, with models for the
CEO, product manager, developer and reviewer roles. These profiles start the
programs with their permission prompts switched off, so agents work without
asking you. If omo was started from a shell whose `PATH` lacks the programs,
use **Detect agent programs** on Settings → Agents later. A profile gets the
argument that makes the program skip its own permission prompts.

## First steps on the empty board

The empty board shows **First steps**:

1. **Add the Superpowers skill set (recommended)**: a public set of skills
   (brainstorming, test-driven development, code review) that your agents are
   told to follow. Optional; see [Skill sets](skills.md).
2. **Add a project**: **Add a workspace folder** (the folder that holds your
   repositories, for example `~/code`) lets omo find every git repository
   in it; **Add project** adds a single repository.
3. **Check the agent setup** (or **Detect agent programs** when omo found
   none): which programs and models each role uses, and what is missing. Fix
   it on Settings → Agents and Settings → Roles.
4. **Register omo in Antigravity (recommended)**, only when an Antigravity
   profile exists and omo's MCP endpoint is on: Antigravity reads omo's MCP
   entry only from its own configuration (see [MCP](mcp.md#how-agents-reach-omo)).
5. **Create a first ticket.**

Each open step has a **×** that dismisses it, and **Hide these steps** hides
the list; both are remembered in that browser. The list disappears once the
board has a ticket.

## The first ticket, end to end

Start with something small in one repository.

1. **Create** (top bar, or key `c`): **Type** Task, a **Title** such as "Add a
   --version flag", the **Project**, and a **Description** of what you want
   and how to know it is done. The ticket appears in **Planning** and waits
   there until you approve it. Its base branch is the branch your project
   folder has checked out now.
2. **Approve** it: drag it to **Ready For Dev**, or press **Approve** in its
   panel. omo starts a developer agent as soon as a slot is free: the card moves
   to **In Progress**, and the sidebar's **Agents** tab lists the agent.
3. **Watch** (optional): open the ticket and its **Agents** tab. You see the
   agent's terminal and can write to it in the message box below (see [Terminals](terminals.md)). The agent works on its own branch
   (`omo/add-a-version-flag`) in a worktree under `OMO_HOME/worktrees/`; your
   project folder is not touched. If it asks you something, the card says
   "question for you" and the bell rings: answer in the Comments tab.
4. **Review**: when the developer hands in, the ticket moves to **Review**. By
   default a reviewer agent checks it first; if it rejects, the developer fixes
   and hands in again. When it approves, the card says "your review" and the
   ticket waits for you.
5. **Look at the change** in the ticket's **Diff** tab.
6. **Approve and merge…**: omo merges the branch into the base branch as a merge
   commit, and the ticket is **Done**. If the base branch is checked out in your
   project folder, omo fast-forwards it there. To send it back instead, use
   **Reject…** with a comment.

That is the whole loop. Next:

- A **story** for a bigger goal: a product manager agent splits it into tasks,
  and the story is merged as a whole when they are done (see
  [Concepts](concepts.md)).
- The **CEO** tab: tell the CEO what you want and it writes the tickets. They
  wait in Planning for your approval (by default: **The CEO's tickets wait for
  your approval** on Settings → General).
- **Export as a pull request** instead of merging locally (see
  [Merging and export](merging-and-export.md)).
- **Your instructions** for each role on Settings → Prompts: your conventions,
  how to run the tests.
