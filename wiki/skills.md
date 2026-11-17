# Skill sets

A skill is a written procedure an agent can follow (a `SKILL.md` with a name
and a description, and whatever files it refers to). A **skill set** is a git
repository of skills that omo tells the agents of chosen roles about. No skill
set is required.

## Adding one

**Settings → Skill sets**:

- **Add Superpowers** adds [obra/superpowers](https://github.com/obra/superpowers)
  (brainstorming, test-driven development, code review and more) for the
  product manager, developer and reviewer. The board's first steps offer it
  too.
- **Add grill-me for the CEO** adds the grill-me skill of
  [mattpocock/skills](https://github.com/mattpocock/skills) for the CEO.
- **Add skill set…** takes any repository.
- **Search skills.sh** finds published skills (see below).

A skill set has:

| Field | Meaning |
|---|---|
| **Repository** | an https or ssh address, or a folder on this computer |
| **Branch or tag** | empty for the default branch |
| **Updates** | **Update at every start** (fetch when omo starts) or **Keep this version (pinned)** |
| **Agents are told about it** | the set's switch |
| **Roles** | CEO, Product manager, Developer, Reviewer, Export; none chosen: every role |
| **Folders and files** | which part of the repository, relative to its root: a folder with a `SKILL.md` is one skill, a folder without one gives every skill below it, a single file is a skill of its own; none: the whole repository |
| **What agents are told** | an optional text of yours per role, put before the list of skills |

The set is stored in `skillsets/<name>.yaml`; omo keeps one read-only checkout
in `OMO_HOME/skills/<name>/` and only ever fetches into it. **Update now**
fetches at once; a failed fetch keeps the last good checkout. The page shows
the commit that is checked out.

## What agents get

omo writes nothing into the programs' own skill folders. Instead each agent's
prompt lists the skills of the sets for its role: name, description and the
path of the file in the checkout, after your text for that role. The agent
reads the file when it needs the skill. This is the same for Claude Code,
Codex, Gemini and Antigravity.

## Searching skills.sh

**Search skills.sh** finds skills published on [skills.sh](https://skills.sh).
It is powered by skills.sh; omo is not affiliated with skills.sh or Vercel.

- omo's server asks skills.sh's search and sends nothing but the search text;
  your browser never contacts skills.sh.
- A search needs at least 2 characters; at most 25 results are shown with name,
  repository and install count, and only skills in a GitHub repository.
- **Use** fetches the skill's repository once, finds the folder whose
  `SKILL.md` names that skill and opens a new skill set with the repository,
  its default branch and that folder. You choose the roles; nothing is stored
  until you save.

Everything on skills.sh is published by anyone and reviewed by nobody at omo:
the form says "Nobody reviewed this skill" and asks again before the first
save. Read the skill's files first.

## What to keep in mind

- A skill is instructions to agents that run as you. Whoever can push to a
  branch a set follows decides part of what your agents are told: pin a
  commit or tag you read.
- Agents run as you and can change the checkout; the next update replaces it.
