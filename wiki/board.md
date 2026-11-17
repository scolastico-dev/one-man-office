# The board

The board is where you work with omo every day: create tickets, approve them,
answer questions, review, and watch the agents. This page goes through it part
by part. What the columns and roles mean is in [Concepts](concepts.md).

- [Columns and cards](#columns-and-cards)
- [Moving cards](#moving-cards)
- [Filters and search](#filters-and-search)
- [Creating a ticket](#creating-a-ticket)
- [The ticket panel and page](#the-ticket-panel-and-page)
- [Comments and questions](#comments-and-questions)
- [Images](#images)
- [The Diff tab](#the-diff-tab)
- [The sidebar](#the-sidebar)
- [Notifications](#notifications)
- [Keys](#keys)

## Columns and cards

The columns are **Planning**, **Ready For Dev**, **In Progress**, **Review** and
**Done**. Stories come before tasks in a column. Done is ordered by closing
time and shows older tickets with **Load older**. Below 768 px the board shows
one column at a time, with tabs to switch; a tab with "!" holds something that
waits for you.

A card shows the ticket's number and title, its project (or "no project"), its
story, the agents working on it ("developer · calm-badger", "parked",
"starting"), a story's progress ("2 of 5 tasks done"), and one badge, the most
urgent of:

| Badge | Meaning |
|---|---|
| needs attention | a step failed or omo flagged a ticket you supervise; the line under it says why (see [Troubleshooting](troubleshooting.md#a-ticket-says-needs-attention)). A ticket another supervisor handles says "needs the CEO" or "needs the product manager" instead, and the bell and Needs me leave it out |
| question for you | an agent asked you something |
| your review | the review waits for you |
| waiting for #N | a dependency is not done yet (" · PR not merged", " · cancelled") |
| waiting for usage | its models are at their usage limits ([Usage](agents.md#usage)) |
| waiting for a task slot, waiting for a story slot, story limit reached | the limits of work in parallel are reached ([Limits](concepts.md#limits)) |
| paused | omo is paused |
| no usable model | its role has no model that can start |

Under the badge a card may say what omo is doing: "agent review",
"merging…", "exporting…", "starting…"; a done card says "cancelled" or
"exported".

## Moving cards

Drag a card, or use its **Move #N…** menu, or focus it and press Space (arrow
keys move, Space drops, Escape cancels). What a move means depends on the
columns:

| Move | Meaning |
|---|---|
| Planning → Ready For Dev | Approve: omo may start it. |
| Ready For Dev → Planning | Withdraw the approval (not once an agent started). |
| In Progress → Planning or Ready For Dev | Stop its agents; you confirm first. Branch and worktree stay. |
| Review → In Progress | Reject; you write what needs to change. Only for a review that waits for you, or one whose merge failed. |
| Review → Done | Approve the review (only when it waits for you). The card stays in Review, "merging…" or "exporting…", until omo is done. |
| Within a column | Change the priority. **Move to top** and **Move to bottom** do the same. |

Everything else is refused with the reason in the menu or under the dragged
card, for example "A ticket enters Review when its agent hands it in." or
"Done tickets stay where they are." To close a ticket without finishing it,
cancel it.

## Filters and search

The top bar filters the board:

- **Project**: all projects, **No project** (tasks without a project) or one
  project.
- **Story**: one story and its tasks; clicking a card's story chip does the
  same.
- **Supervisor**: Anyone, You, CEO or Product manager.
- **Needs me**: tickets with a question for you, a review that waits for you,
  or a flag on a ticket you supervise.
- **Search** (`/`): numbers and titles at once, descriptions through the
  server.

The filters are part of the address (`?project=`, `story=`, `supervisor=`,
`needs_me=1`, `q=`), so a filtered board can be bookmarked. **Clear filters**
resets them.

## Creating a ticket

**Create ticket** (key `c`), or **Add to Planning** in the Planning column, or
**Add task** on a story. The dialog asks for:

- **Type**: **Story** (a goal that is split into tasks) or **Task** (one piece
  of work in one project, or in none).
- **Title**, at most 200 characters.
- **Story (optional)** for a task.
- **Projects** for a story, **Project** for a task: a project, or **No
  project** for research or an experiment that changes no repository
  ([Tasks without a project](concepts.md#tasks-without-a-project)).
- **Description**, in Markdown. A story starts from a template with the
  headings Goal, Do, Don't and Done when.
- **More options**: **Supervisor**, **Model** (default: the role's models),
  **Review**, **When approved** (Merge locally or Export as a pull request),
  **Tasks are released by** and **Max parallel tasks** for a story, and **Depends on**
  (ticket numbers).

A new ticket starts in Planning. Its base branch is the branch the project's
folder has checked out at that moment; the dialog warns when that is not the
repository's default branch. Change it later in the ticket's details to
another branch of the repository (a commit, tag or expression such as
`main~3` is refused). Images
can be added once the ticket exists.

## The ticket panel and page

A click on a card opens the ticket's panel over the board (`/<number>`). **Open
as page** (or `p`) shows it on a page of its own (`/ticket/<number>`), with the
tabs at the top and the details in a column on the right; **Open in panel**
(`p`) goes back and **Back to board** (`b`) returns to the board with its
filters. A middle click or Ctrl-click on any ticket link opens the page in a
new tab. With **Open tickets on their own page** (Settings → General) a click
opens the page instead of the panel.

The ticket shows:

- **The header**: number, column, title (editable until Done), its badge and
  the pull requests of an exported ticket.
- **Needs attention**: what failed and why, with the button that retries it.
- **Actions**: **Approve** (in Planning), **Approve and merge…**, **Approve and
  export…** or **Approve…** (a review that waits for you), **Reject…**,
  **Export…**, **Merge locally instead** (inside the approve dialog when the
  ticket would export), **Retry start**, **Retry agent** or **Retry merge**,
  **Mark as merged** (an exported ticket whose pull request you merged), **Start
  anyway…** (waiting for usage), **Open terminal**, **Cancel ticket…**, and the
  buttons of plugins.
- **Details**: Project, Story, Depends on, Blocks, Supervisor, Model, Review,
  When approved, Tasks are released by and Max parallel tasks (stories), Working folder
  (a task without a project), Branch, Base branch, dates and the description's
  version. A field that an agent's work depends on is locked with the reason
  ("Locked while the branch omo/login exists."). Changing a field while an
  agent works tells the agent in a comment.
- **The description**, with **Edit** and its **History**.
- **Tabs**: **Comments**, **Diff** (not for a task without a project),
  **Agents** (every agent of the ticket: a running one's terminal, an ended
  one's log and why it ended) and **Tasks** (stories). The page adds
  **Overview** first.

**Cancel ticket…** closes it as cancelled and stops its agents; a story's open
tasks are cancelled with it. Choose **Keep the branch** (the branch and its
worktree stay) or **Delete the branch** (omo keeps folder and branch when the
worktree has uncommitted changes and lists them in a comment).

**Remove from ticket** under Branch takes a project off a ticket that already
has a branch there (only while the ticket has more than one project and is in
Planning, Ready For Dev or a review that waits for you). omo removes the
worktree and deletes the ticket's own branch there, and asks again before it
throws away commits the base lacks or uncommitted changes.

## Comments and questions

Comments are Markdown, at most 64 KiB. A mention reaches the agent behind a
role: `@developer`, `@reviewer` (on a ticket with several projects also
`@reviewer:<project id>` for one project's reviewer), `@pm`, `@supervisor`,
`@ceo` and `@user`; typing `@` offers them. A mention of a role nobody works in
right now is stored, and the box says so. **Ctrl+Enter** (**Cmd+Enter** on a Mac) sends.

**Ask a question** sends the comment as a question to the product manager or
the CEO; it stays open until answered. A question an agent asks you shows
"Question for you" and an **Answer** button; your answer reaches the agent.

The thread also holds omo's status lines: moves, review verdicts ("Review
approved by reviewer … (round 2)"), merges, exports, agents that started and
ended.

## Images

Comments and descriptions can carry images: **Add image…** under the text box,
a paste, or a drop onto the box. PNG, JPEG, GIF and WebP up to 10 MiB, at most
100 per ticket and 2 GiB for all of omo; SVG is refused. While a file uploads
a placeholder line shows where it goes; then it becomes
`![screenshot.png](omo-image:<id>)`. A click opens an image full size.

omo keeps images in `OMO_HOME/images/` under random names and deletes a
ticket's images the number of days after it is done or cancelled that
**Settings → Limits** sets ("Keep images after Done", 60 by default, 0 keeps
them); the text then says "image expired". Agents add images with
`omo ai image add`, and when they read a ticket they get the local path of
each image. An export does not upload images: the pull request names each as
"[image: <name>, not exported]".

Images dropped into a terminal are a different thing; see
[Terminals](terminals.md#images).

## The Diff tab

The Diff tab shows, per project, the commits on the ticket's branch since it
left its base (its base branch, or the story's branch for a task in a story)
and the changes not yet committed in the worktree. The panel stacks the files;
the page has a file browser with a filter and **Previous file** / **Next
file**. Large diffs are cut and say so.

- Hidden lines between the changes: **Show 20 hidden lines**, **Show all N
  hidden lines**, or **Show the whole file** in a file's header (not for
  binary, new, deleted or untracked files, nor for one whose diff would be over
  1 MiB).
- omo never fetches. When the local base branch is behind its remote-tracking
  branch, the tab says so ("local main is 4 commits behind origin/main, …") and
  offers **Compare with origin/main** for this ticket until the page is
  reloaded.
- **Done tickets**: when a ticket is merged or exported, omo records the two
  commits its diff ran between, and keeps them in the repository under
  `refs/omo/tickets/` even after the branch is deleted or squashed. The tab then
  shows "Changes as recorded when the ticket was done", and the Branch field
  says "Deleted after the merge" and "Diff recorded when done: abc1234..def5678".
  When those commits are no longer in the repository, or for a ticket done
  before omo recorded them, the tab says so.

## The sidebar

The sidebar next to the board has three tabs. On a narrow screen it opens as a
sheet from the top bar.

**CEO**: the CEO's terminal ("Typing here types into the CEO."). Choosing the
tab starts the CEO when it is not running, unless you stopped it. **Stop…**
stops it until you press **Start the CEO**: mentions and questions for it wait
until then, also across a restart of omo. **New session…** ends its session
and starts a fresh one, which reads the open tickets it supervises.
**Model: …** picks the CEO's model (it writes the CEO's model list under
Settings → Roles); a running CEO keeps its model until it starts again, or
until **Restart now**. The tab is hidden when the CEO is switched off
(Settings → General). What the CEO does is in
[Concepts](concepts.md#the-ceo).

**Agents**: the usage meters of your accounts (see
[Usage](agents.md#usage)), "In work: 2 of 4 tasks · 1 of 2 stories", and every
running agent with role, name, ticket, project and model; open one to see its
terminal. An agent that used omo's MCP tools carries the chip **MCP**.

**Terminals**: your own shells (see [Terminals](terminals.md)). They keep
running when the browser closes.

## Notifications

The bell lists what waits for you (questions, reviews, flagged tickets) and
mentions of you, with **Mark all as read**. **Desktop notifications** switches
on browser notifications in that browser. To get them on your phone, install
the pushover or discord plugin ([Plugins](plugins.md)).

## Keys

| Key | Where | Does |
|---|---|---|
| `c` | board | Create ticket |
| `/` | board | Search |
| Space or `m` | a focused card | Pick it up; arrows move, Space or Enter drops, Escape cancels |
| `p` | ticket | Panel ⇄ page |
| `b` | ticket page | Back to the board |
| Escape | panel | Close it |
| Ctrl+Enter or Cmd+Enter | comment box, description | Send, save |

The keys do nothing while you type in a field, a search or a terminal. Keys in
terminals are on [Terminals](terminals.md).
