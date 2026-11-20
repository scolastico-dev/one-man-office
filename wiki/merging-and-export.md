# Merging and export

When a ticket's review is approved, its work leaves omo in one of two ways:
omo merges it into the base branch on this machine, or it exports it as a pull
request per repository. The ticket's **When approved** setting decides:
**Merge locally** or **Export as a pull request** (default: Settings → Review,
Merge locally). A task in a story always merges into the story's branch; the
story's setting decides how the work leaves omo.

- [Merging locally](#merging-locally)
- [Merge commit or squash](#merge-commit-or-squash)
- [Exporting as pull requests](#exporting-as-pull-requests)
- [Branch names](#branch-names)

## Merging locally

omo builds the merge without touching any checkout: it computes the merge
commit inside git and moves the target branch to it. Your project folder is
changed only when it has the target branch checked out: then omo runs
`git merge --ff-only` there (without hooks), and only after checking that
nothing is in the way. omo never runs `merge --abort`, `reset` or `checkout` in
your folder and never edits your git config.

- **A conflict** sends the ticket back to In Progress with a comment that
  lists the files; the developer merges the base into its branch, resolves it
  and hands in again. A story's conflict asks for a task that resolves it.
- **Something in your folder in the way** (uncommitted changes to the merged
  files, a rebase in progress) flags the ticket: "Uncommitted changes to … in
  … block merging into main. Commit, stash or discard them there, then retry."
  Fix it and press **Retry merge**.
- **Commits added after the hand-in**: omo merges only what was reviewed.
  When the branch moved after the hand-in, it merges nothing and asks: retry
  (merge the reviewed commit, leave the rest) or reject (review the new commits
  too).

A merged ticket is Done (completed). Its branch is deleted when it is merged;
the commits its diff ran between stay recorded (see
[The Diff tab](board.md#the-diff-tab)). omo signs its merge commits when
`commit.gpgsign` is set for it (the agent environment on Settings →
Environment sets `commit.gpgSign=false` by default).

## Merge commit or squash

There are three kinds of merge, and each has its mode: a story into its base
branch, a task without a story into its base branch, and a task into its
story's branch. **Settings → General → Merge defaults** sets them for all
projects (all **Merge commit** by default); a project overrides them on
**Settings → Projects** (Edit, "Use global default (…)" or its own mode).

- **Merge commit**: a commit titled "Merge: <title>".
- **Squash into one commit**: one commit titled "<title>"; a story's squash
  lists its completed tasks.

No commit message omo writes contains a ticket number, which a forge would
read as one of its own issues.

## Exporting as pull requests

With **Export as a pull request**, or **Export…** on a ticket whose review
waits for you, omo pushes and an export agent opens a pull request per
repository:

1. A project whose branch has no commits of its own is left out of the export
   (it has nothing to export).
2. For every other project omo pushes the commit that was reviewed to the
   repository's push remote (`branch.<base>.pushRemote`, `remote.pushDefault`,
   `branch.<base>.remote`, then `origin`), as the ticket's branch. It never
   replaces a foreign remote branch and runs no hooks. A reviewed re-export
   can replace omo's own earlier push after a rebase; it proceeds only while
   the remote branch still matches that recorded push. Your credentials for
   that remote are used as git finds them. After a rejection, a later export of the ticket
   moves the same remote branch on and edits the pull request that exists.
3. An export agent runs per project in a folder under `OMO_HOME/run/export/`.
   omo gives it `material.md` (the ticket, a story's tasks, this repository's
   commits and changed files, the other repositories of the ticket and their
   pull requests) and lets it read the repository. It writes a title and a
   description **for this repository**: what this repository's change does
   and why, how to verify it and what the reviewer should know, following the
   repository's pull request template when it has one, with one line naming
   the other repositories when the ticket has several.
4. `omo ai export-text` checks the text; a missing, empty, too long or
   ticket-numbered text is replaced by omo's own (the ticket's title and
   description, with the commit subjects), so a bad text never stops the
   export.
5. The agent opens the pull request with `gh` (GitHub), `glab` (a host whose
   name starts with `gitlab`) or `tea` (any other host), or updates the one
   that exists for the branch, and reports its address. omo itself never runs
   these tools: they must be installed and signed in for your user.

While omo exports, the card says "exporting…"; other changes to the ticket
are refused with that word until it is done.

A comment links each pull request and says whether the agent or omo wrote its
text. When every project has its pull request, the ticket stays in Review,
marked **Exported, waiting for the merge** (`awaiting_merge`). Its details show
the recorded remote, exported branch, target branch and pull request. Its
resolution stays open and dependencies keep waiting until the exported work
has merged into every remote target branch.
When one fails, the ticket stays in Review, flagged "Export of <project>
failed: <reason>"; the exported projects keep their pull request. Then either
approve the export again (only projects without a pull request are tried),
choose **Merge locally instead** (merges only those; the ticket still waits for
the projects with pull requests to merge remotely), or take the failing
project off the ticket (**Remove from ticket**).
Removing a failed project leaves Attention showing only the failures of the
projects still on the ticket, even when the earlier message was shortened.

A repository whose remote is a folder on this machine has no forge, so its
export fails; give it a real remote (`git remote add origin <address>`).

Images are not uploaded: a pull request names each as "[image: <name>, not
exported]".

### Follow-up work after export

Use **Send follow-up task…** while an exported ticket waits for its merge or
for human review. Write the requested work and choose **Developer** (the
product manager for a story) or **Reviewer**. The developer resumes work on
the existing branch; a reviewer gets a fresh round for the requested
verification and can send code changes back to the developer.

The previous pull request link stays available. Its old merge watch is
invalidated, so an old result cannot close the new work. After the developer
commits and hands in, the usual review policy applies: agent review, human
review, or automatic re-export. A requested reviewer task also returns to
human review when that is the ticket's policy.

At human review, **Re-export…** replaces **Export…**. If the reviewed local
commit differs from the last exported commit, the approval dialog warns that
the remote branch still contains the previous version. Approval asks omo to
push the reviewed commit and update the existing pull request. A rebase can
require replacing the earlier history; omo uses an exact lease against its
last recorded push and refuses if somebody else moved or deleted that remote
branch. Commits added after the hand-in are not included.

omo records the new exported commit and remote target baseline. Merge checks
then recognize that revision through a merge commit, squash, or fast-forward.
Give rebase and follow-up requests through this workflow so omo can keep an
exact record of what was reviewed and pushed.

The user CLI offers the same action:

```bash
omo ai ticket follow-up N --role developer --comment-file - <<'EOF'
Rebase onto origin/main and resolve the conflicts. Hand in the result for review.
EOF
```

**Checking for a merge**: omo checks the remote target branches with Git,
without calling a forge API. It uses the remote, target branch and reviewed
commit recorded when the work was exported, so later configuration changes
do not redirect the check. Fetching the recorded target keeps the checkout,
index, local branches and remote-tracking branches intact.
A normal merge can be found by commit ancestry; squash and rebase merges can
be found by checking that the exported content is present. If the original
commit does not prove inclusion, omo also checks the current local tip of the
recorded exported branch. This detects work rebased with conflict resolutions
or extended by follow-up commits before merging, including squash merges.
The fallback excludes upstream changes brought in by a rebase; discarding the
feature's changes during conflict resolution does not count as a merge.
A nonempty changed patch observed before landing is retained across restarts,
so a subsequent fast-forward can also be recognized. A rewritten branch that
was already fast-forwarded before omo observed or re-exported it can remain
inconclusive; the managed follow-up workflow avoids this ambiguity.
The original export and diff records stay intact, and remain usable if the
branch is deleted. Changes made only in another clone must also be available
on the local exported branch for this fallback. These content checks use Git's
built-in text merge independently of the project's merge-driver policies.
Content checks are conservative: later rewrites,
reverts or conflicts can leave a merged ticket waiting. A failed or unavailable
remote also leaves the ticket waiting.

**Settings → Limits → Export check interval** controls automatic checks
(`export_check_interval`, default `10m`; `0` turns them off, otherwise at least
`1m`). This is independent of workspace rescanning. **Check now** on a waiting
ticket requests a check; the user CLI `omo ai ticket check-export N` does the same. Repeated requests share the same checking round. The
ticket updates when the check finishes. A confirmed merge moves the ticket to
Done (exported, merged) and lets its dependents start.

You can also use **Mark as merged**, or move the waiting card to Done, to
confirm completion yourself. **Reject…** sends it back to In Progress for
further work. Existing Done exports from older versions still support
**Mark as merged**. When all projects have nothing to export, the ticket goes
straight to Done (completed).

## Branch names

omo names a ticket's branch by **Settings → General → Branch name template**
(`branches.template`), `omo/{slug}` by default.

| Placeholder | Value |
|---|---|
| `{slug}` | the title in lower case with dashes, at most 40 characters (`ticket` when it has no letter or digit) |
| `{number}` | the ticket number |
| `{type}` | `story` or `task` |
| `{project}` | the project |
| `{host}` | this machine's name |
| `{user}` | your user name on it |

Other text is kept as typed, so `feature/{number}-{slug}` or
`{user}/{type}/{slug}` work. The template must contain `{slug}` or `{number}`
and give valid git branch names of printable ASCII; the page shows an example
while you type and refuses a template it cannot use. A name that is taken (a
local or remote-tracking branch, a branch of another ticket in the same
repository, the base branch, `main`, `master`, a remote's default branch) gets
`-2`, `-3` and so on; names are compared without regard to case. A ticket keeps
the branch it has when you change the template.

omo pushes or deletes only the branch recorded for that ticket, never a base or
default branch.
