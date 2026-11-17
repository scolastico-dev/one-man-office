# Concepts

How omo organises work: tickets, columns, roles, reviews and the rules that
decide when an agent starts. The pages on [the board](board.md) and
[merging and export](merging-and-export.md) show where you act on these.

- [Tickets: stories and tasks](#tickets-stories-and-tasks)
- [Columns](#columns)
- [Roles](#roles)
- [The CEO](#the-ceo)
- [Supervisors and questions](#supervisors-and-questions)
- [Approval and release](#approval-and-release)
- [Review rounds and verdicts](#review-rounds-and-verdicts)
- [Tasks without a project](#tasks-without-a-project)
- [Limits](#limits)
- [Pause and stop all agents](#pause-and-stop-all-agents)
- [Restarts](#restarts)

## Tickets: stories and tasks

omo has one board for all your repositories. A **project** is one git
repository on this machine (see [Settings → Projects](settings.md#projects)).

- A **task** is one piece of work in one project, or in none. One developer
  agent does it on a branch of its own, in a git worktree omo makes under
  `OMO_HOME/worktrees/`. When it is approved, the branch is merged into the
  task's base branch, or exported as a pull request.
- A **story** is a goal across one or more projects. A product manager agent
  splits it into tasks. The story has a branch of its own per project, cut from
  the base branch; its tasks are cut from the story's branch and merged back
  into it. When every task is done, the story is reviewed as a whole and merged
  into its base branches (or exported).

A task can stand alone or belong to a story. A ticket can depend on others
(**Depends on**): it does not start until they are done (completed, or
exported and its pull request merged). A cancelled dependency keeps it waiting.

The **base branch** of a ticket is the branch its project's folder had checked
out when the ticket was created; change it in the ticket's details before work
starts. A task in a story has none: it builds on the story's branch.

## Columns

| Column | What it means |
|---|---|
| **Planning** | The ticket is being written. Nothing runs. |
| **Ready For Dev** | Approved: omo starts it as soon as the limits allow. |
| **In Progress** | Its agent works (or waits, or is stopped by a limit; the ticket keeps its slot). |
| **Review** | Handed in: reviewer agents and/or you check it; then omo merges or exports. |
| **Done** | Completed, exported or cancelled. Done tickets cannot be changed. |

A ticket enters In Progress when its agent starts or its review is rejected,
and enters Review when its agent hands it in; you do not drag it there. What
you can drag is in [Moving cards](board.md#moving-cards).

## Roles

omo runs agent programs (Claude Code, Codex, Antigravity, Gemini; see
[Agents](agents.md)) in five roles:

| Role | What it does |
|---|---|
| **CEO** | Talks with you in the CEO tab and turns your requests into tickets. Never changes code. |
| **Product manager** | One per story: writes the story's tasks, releases them, watches them, hands the story in. Works in the story's worktree but does not commit there. |
| **Developer** | One per task: writes the code and its tests in the task's worktree, commits, and hands in with `omo ai done`. |
| **Reviewer** | One per project and review round: reads the work and gives a verdict (`omo ai done --approve` or `--reject` with a comment). |
| **Export** | One job per project when a ticket is exported: writes the pull request's title and text and opens it (see [Export](merging-and-export.md#exporting-as-pull-requests)). It uses the developer role's models. |

You are the user: you approve, review, answer questions and decide when a step
needs a person. Each role's models are set on
[Settings → Roles](settings.md#roles), and its instructions on
[Settings → Prompts](settings.md#prompts).

Agents work through `omo ai` (or the same commands as [MCP](mcp.md) tools):
`omo ai ready` gives them their instructions, `omo ai wait` parks them until
something arrives (an answer, a review result), `omo ai done` hands in.

**Kept agents.** The developer of a task (the product manager of a story) keeps
its session from In Progress until Done: parked during the review, woken by a
rejection, a merge conflict or a mention. A reviewer stays parked after its
verdict, so you or the developer can ask it about its review with `@reviewer`,
until the next review round of its project or until the ticket is done. Parked
agents hold no slot.

## The CEO

The CEO is an agent you talk to in the sidebar's **CEO** tab, like a terminal
session with the agent program. Tell it what you want; it reads the board and
creates stories and tasks in Planning. It does not change code, merge or work
on tickets.

- It starts only when you open its tab, press **Start the CEO**, or an agent
  asks it something (at most once in 10 minutes, not while paused).
- **Stop…** stops it until you start it again, also across restarts of omo.
- It counts against no limit and starts even while omo is paused.
- **The CEO's tickets wait for your approval** (Settings → General, on by
  default): the stories and tasks it creates stay in Planning until you approve
  them, and it cannot approve or withdraw them itself. Switched off, it may
  release standalone tasks it created (when tasks are released by agents), and
  stories only with **The CEO may approve stories**, after you agreed in its
  terminal.
- **The CEO supervises the board** off hides the tab, ends a running CEO and
  hands everything it supervised to you.

## Supervisors and questions

Every ticket has a supervisor: **you**, the **CEO**, or the **product
manager** (tasks in a story). By default a ticket is supervised by whoever
created it (a task in a story by the story's product manager). The supervisor
is told when the ticket needs attention, and may retry its failed steps.

Agents ask with `omo ai ask` (in the browser: **Ask a question**). A question
goes to the ticket's supervisor unless it names someone (`--to pm|ceo|user`);
the supervisor itself asks one step up. It stays open until the addressee or
you answer, and the answer is typed into the asking agent. A question to you
shows as "question for you" on the card and in the bell. An unanswered
question is flagged after **Question unanswered after** (Settings → Limits);
a question to an agent first gets a reminder.

## Approval and release

**Approving** moves a ticket from Planning to Ready For Dev: drag it, use
**Approve**, or `omo ai ticket move N ready`. Before that nothing runs. A
story needs a project; a ticket needs a base branch. **Withdraw** (back to
Planning) is possible until an agent started.

Who may approve:

- You: every ticket.
- The CEO: see [The CEO](#the-ceo).
- A product manager **releases** the tasks it created in its story, unless
  **Tasks are released by** is **You** (Settings → Review, or per story
  in its details). Then you release each task.

Tasks of a story start only while the story is In Progress, that is once its
product manager runs.

**Approving a review** is the other approval: when a review waits for you (the
card says "your review"), **Approve and merge…** or **Approve and export…**
(or drag to Done) lets omo merge or export it; **Reject…** sends it back to
In Progress with your comment. Which reviews wait for you is the ticket's
**Review** setting:

| Review | What happens at hand-in |
|---|---|
| **Skipped** | Merged or exported at once. |
| **Agent** | Reviewer agents decide; their approval merges or exports. |
| **Human** | You decide. |
| **Agent, then human** | Reviewer agents first; when they approve, you decide. |

Defaults (Settings → Review): Agent, then human for stories and for tasks
without a story; Agent for tasks in a story. With Skipped or Human on a story,
no agent runs the full test suite for the story as a whole.

## Review rounds and verdicts

Each hand-in starts a **review round**. A reviewer agent works per project. The
round ends only when every project has a verdict; then one comment says that
the round is over, lists the approvals and quotes each rejection for the
developer or product manager, and the ticket goes back to In Progress (a
rejection) or on (all approved).

- A project whose approved commit of an earlier round is unchanged needs no
  reviewer: its approval carries over. So does a story's project whose branch
  has no commits of its own. One comment per round names these projects. A
  rejection never carries over.
- A reviewer that stays silent without a verdict is reminded once after
  **Silent after** (Settings → Limits) and flagged after the same time again.
- After more than 5 rejections, or 5 merge conflicts, since a person last acted
  on the ticket, omo stops and flags it: "#12 has gone back and forth: …".

## Tasks without a project

A task can have no project: for research, an investigation across
repositories, or a local experiment that changes no repository. Choose **No
project** when you create it (`--no-project` on the command line); a task never
loses its project by accident, and a story always needs one.

Its developer works in a plain folder omo makes, `OMO_HOME/workdirs/<ticket>`,
and is told the folders of your projects to read but never change. It hands in
its findings with `omo ai done`; the review judges the summary, the comments
and the files in the folder. Approving it moves it to Done: nothing is merged
or exported, so **When approved**, the base branch and export do not apply.
The card shows "no project", the ticket shows its working folder instead of a
branch, and there is no Diff tab. **Open terminal** opens a shell in the
folder, also after Done. The folder stays after Done and after a cancel;
**Remove folder** (`omo ai ticket remove-workdir N`) deletes it.

## Limits

**Settings → Limits → Work in progress**: **Tasks in work** (4) and **Stories
in work** (2). A ticket takes a slot when it moves to In Progress and gives it
back when it is Done, or when it is moved back to Ready For Dev or Planning;
waiting, review and questions do not free it. Tasks in a story count too. A
story can also limit its own parallel tasks (**Max parallel tasks**). The CEO never
counts, and agents that continue started work (reviewers, export jobs, a
developer after a rejection) are never held back. Lowering a limit stops
nothing. A ticket that waits shows "waiting for a task slot", "waiting for a
story slot" or "story limit reached".

Usage limits of your accounts are separate; see [Usage](agents.md#usage).

## Pause and stop all agents

**Paused** (the switch in the top bar, "Running" / "Paused"): omo starts no
agent at all, not for new work and not to continue started work. Agents that
already run go on working, merges and cleanups go on, and you can still start
the CEO. The switch survives restarts. To have running agents halt as well,
install the [pause-notice plugin](plugins.md#the-plugins-omo-ships).

**Stop all** ("Stop every running agent?") pauses omo and ends every running
agent at once, without asking for a handoff. A running CEO keeps running. Tickets
keep their columns; work stays in the worktrees. When you switch the pause off,
started work gets fresh agents.

To stop one ticket, drag it from In Progress back to Ready For Dev or Planning
(**Stop and move**): its agents end, branch and worktree stay.

## Restarts

`omo stop` asks every working agent for a handoff comment ("Handoff: …") and
waits up to **Handoff time limit at stop** (2 minutes). At the next start, omo
ends what is left of the old agents and starts a fresh agent for every ticket in
work (also beyond the limits, but not while paused). The fresh agent is told to
run `git status`, keep every uncommitted change and read the handoff. Parked
agents are not restarted just to wait: the next round gets a fresh reviewer. A
crash of omo works the same way, without the handoff.

Failure counts and **Start anyway** are forgotten on a restart; the pause, a
stopped CEO and review approvals are kept.
