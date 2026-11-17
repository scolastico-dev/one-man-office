# Terminals

Every agent omo runs is a real agent program in a terminal, and the board shows
that terminal: in the CEO tab, in a ticket's Agents tab, and in a window of its
own. You can also open your own shells. Typing into an agent's terminal types
into the agent, exactly as if you sat at it.

- [Your own shells](#your-own-shells)
- [Typing into an agent](#typing-into-an-agent)
- [Copy and paste](#copy-and-paste)
- [Images](#images)
- [A terminal in its own window](#a-terminal-in-its-own-window)

## Your own shells

The **Terminals** tab of the sidebar holds your shells. **New terminal** opens
a search over your projects (type part of a name or folder); **Home folder** is
always first. A ticket's **Open terminal** action opens a shell in its worktree,
or in the working folder of a task without a project (also after it is done).

- At most 16 of your shells run at once.
- A shell in a ticket's worktree keeps that worktree: the ticket can still be
  merged and finish, and omo removes the folder when the last shell in it ends.
- Your shells do not act as an agent: `omo ai` in them acts as you.

Only you can open shells and read terminals and agent logs; agents cannot.

## Typing into an agent

- **Enter** submits, as in the program itself.
- **Shift+Enter** and **Ctrl+Enter** insert a line break without submitting.
  The browser cannot send these keys to a terminal program as such, so omo
  sends them as Alt+Enter, which Claude Code, Codex, Gemini and Antigravity
  all read as a line break.
- **F6** moves the focus out of the terminal, **Shift+F6** to the element
  before it.
- omo's own messages to an agent (a mention, an answer, a review result) are
  typed in as one line starting with `omo: `. omo holds such a line back while
  you type in that terminal, so it never ends up inside your text.

An ended agent's terminal shows its saved log instead.

## Copy and paste

Copying works the same in every terminal view: the CEO, an agent, your shells,
a ticket's Agents tab and a terminal's own window.

- **Programs that use the mouse** (Claude Code, Codex, tmux) select in their
  own screen and hand the text to the terminal; omo puts it into the clipboard
  and shows **Copied** in the terminal's corner. omo accepts this only within
  5 seconds of a key or click of yours in that terminal, and never answers a
  program that asks to read the clipboard.
- **The terminal's own selection** (a plain drag in a shell; Shift and drag
  while a program holds the mouse) is copied with **Ctrl+C** (the selection is
  then cleared, so the next Ctrl+C interrupts the program as usual; without a
  selection Ctrl+C is the interrupt), **Ctrl+Insert** or **Ctrl+Shift+C**.
  Firefox may open its inspector on Ctrl+Shift+C; use Ctrl+Insert there.
- **Paste** with **Ctrl+V**, **Ctrl+Shift+V** or **Shift+Insert**. Pasted text
  is cleaned of control characters first.
- **On a Mac**, Cmd+C and Cmd+V copy and paste, and Ctrl keeps its meaning for
  the program.

Over plain HTTP on a host name other than `localhost` (see
[Reaching omo from another machine](running.md#reaching-omo-from-another-machine))
the browser lets a page copy only during a click or key press. When it refuses,
a **Copy selection** button appears in the terminal's corner, and your next key
or click in the terminal copies the text too.

A link in a terminal whose text is not its address asks before it opens
("Open this address?").

## Images

Drop image files onto a terminal, or paste an image you copied in another
program (Ctrl+V with no text in the clipboard): omo stores each image in
`OMO_HOME/images/terminal/` and pastes its full path into the program, quoted
when it has spaces, several separated by spaces, without a line break. Claude
Code and Codex then attach the image.

- PNG, JPEG, GIF and WebP up to 10 MiB each are taken; other files are refused
  with a note in the terminal. All terminal images together may take 2 GiB.
- A read-only or ended terminal takes nothing.
- These images belong to no ticket. They are deleted the number of days after
  their upload that **Settings → Limits** sets for images ("Keep images after
  Done", 60 by default; 0 keeps them).

Images in comments and descriptions are described in
[The board](board.md#images).

## A terminal in its own window

The button in a terminal's top bar opens it alone in a browser window (not
below 768 px wide, and not for an ended terminal). The window remembers its
size per kind of terminal. Its address is `/terminal/agent/<id>` or
`/terminal/user/<id>`, never with the login token.

While the window is open, the board shows "This terminal is open in its own
window." with **Bring it back** and **Show the window**, and holds no connection
to it. When a browser blocks the window, the terminal says so and offers a
link to open it in a new tab.

Within one browser window only one view of a terminal is live, the one opened
last; another view of it says "This terminal is shown in …" with
**Show it here**.
